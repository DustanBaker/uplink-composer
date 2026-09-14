// Package fsimg builds raw disk images (partition table + FAT32 + files)
// entirely in userspace via go-diskfs — VolumeBuilder implementation #1.
//
// The image is later raw-written to a USB device by internal/flash. UEFI
// firmware boots efi/boot/bootx64.efi straight off the FAT32 partition, so no
// boot sector code is ever written (UEFI-only is a product constraint).
package fsimg

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition"
	"github.com/diskfs/go-diskfs/partition/gpt"
	"github.com/diskfs/go-diskfs/partition/mbr"
)

const (
	// SectorSize is the only logical sector size supported in v1. Flash
	// refuses 4Kn sticks with a clear message.
	SectorSize = 512

	// partStartSector leaves the conventional 1 MiB gap before partition 1.
	partStartSector = 2048

	// gptReserveSectors is the backup GPT header + entries at end of disk
	// (33) plus one so the last usable sector stays clear of it.
	gptReserveSectors = 34

	// MaxFileSize is the FAT32 per-file limit (2^32 - 1 bytes).
	MaxFileSize = int64(1<<32 - 1)
)

// FS is the filesystem handle passed to populate callbacks; an alias so
// packages above fsimg never import go-diskfs directly (keeps the backend
// swappable per the VolumeBuilder contingency plan).
type FS = filesystem.FileSystem

// Scheme selects the partition table type.
type Scheme string

const (
	SchemeMBR Scheme = "mbr"
	SchemeGPT Scheme = "gpt"
)

// Options configure BuildImage.
type Options struct {
	Scheme Scheme
	Label  string // FAT32 volume label, e.g. "ESD-USB"
	// SizeBytes is the total image size (multiple of 512). Use
	// SizeForContent to derive it from staged content.
	SizeBytes int64
	// Reproducible fixes FAT timestamps per SOURCE_DATE_EPOCH so identical
	// inputs produce identical images.
	Reproducible bool
}

// SizeForContent estimates a total image size for the given content: bytes on
// disk plus generous per-entry metadata/cluster slack, FAT tables, 10% free
// slack, and the leading 1 MiB gap — rounded up to 4 MiB, minimum 256 MiB
// (safely above the FAT32 minimum cluster count).
func SizeForContent(contentBytes int64, entries int) int64 {
	s := contentBytes
	s += int64(entries) * 64 * 1024 // cluster tail + directory entry slack
	s += s / 10                     // free-space slack
	s += s/64 + 16*1024*1024        // FAT tables + reserved/backup structures
	s += partStartSector * SectorSize
	const min = 256 * 1024 * 1024
	if s < min {
		s = min
	}
	const round = 4 * 1024 * 1024
	return (s + round - 1) / round * round
}

// BuildImage creates imgPath (replacing any existing file), partitions it per
// opts, formats partition 1 as FAT32, and hands the mounted filesystem to
// populate. On any error the partial image is removed.
func BuildImage(imgPath string, opts Options, populate func(fsys FS) error) (err error) {
	switch {
	case opts.SizeBytes <= 0:
		return fmt.Errorf("fsimg: SizeBytes must be positive")
	case opts.SizeBytes%SectorSize != 0:
		return fmt.Errorf("fsimg: SizeBytes %d is not a multiple of %d", opts.SizeBytes, SectorSize)
	case opts.Scheme != SchemeMBR && opts.Scheme != SchemeGPT:
		return fmt.Errorf("fsimg: unknown partition scheme %q", opts.Scheme)
	}
	if err := os.Remove(imgPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fsimg: replacing %s: %w", imgPath, err)
	}
	d, err := diskfs.Create(imgPath, opts.SizeBytes, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("fsimg: creating image: %w", err)
	}
	defer func() {
		if d != nil {
			d.Close()
		}
		if err != nil {
			os.Remove(imgPath)
		}
	}()

	table, _ := partitionTable(opts)
	if err = d.Partition(table); err != nil {
		return fmt.Errorf("fsimg: writing partition table: %w", err)
	}
	fsys, err := d.CreateFilesystem(disk.FilesystemSpec{
		Partition:    1,
		FSType:       filesystem.TypeFat32,
		VolumeLabel:  opts.Label,
		Reproducible: opts.Reproducible,
	})
	if err != nil {
		return fmt.Errorf("fsimg: formatting FAT32: %w", err)
	}
	if err = populate(fsys); err != nil {
		return err
	}
	closeErr := d.Close()
	d = nil
	if closeErr != nil {
		err = fmt.Errorf("fsimg: finalizing image: %w", closeErr)
		return err
	}
	err = fixRootDotDot(imgPath, partStartSector*SectorSize)
	return err
}

// partitionTable is the one partition every image has, and its size in
// sectors.
func partitionTable(opts Options) (partition.Table, int64) {
	totalSectors := uint64(opts.SizeBytes / SectorSize)
	switch opts.Scheme {
	case SchemeGPT:
		end := totalSectors - gptReserveSectors
		return &gpt.Table{
			ProtectiveMBR: true,
			Partitions: []*gpt.Partition{{
				Index: 1,
				Start: partStartSector,
				End:   end,
				Size:  (end - partStartSector + 1) * SectorSize,
				Type:  gpt.EFISystemPartition,
				Name:  "EFI system partition",
			}},
		}, int64(end - partStartSector + 1)
	default:
		return &mbr.Table{Partitions: []*mbr.Partition{{
			Index:    1,
			Bootable: true,
			Type:     mbr.Fat32LBA,
			Start:    partStartSector,
			Size:     uint32(totalSectors - partStartSector),
		}}}, int64(totalSectors - partStartSector)
	}
}

// StageMap maps image paths (forward-slash, leading "/") to host source file
// paths. Later additions overwrite earlier ones, which is how overlays
// (unattend, ei.cfg, $OEM$ payload) replace base-media files.
type StageMap map[string]string

// AddTree walks hostRoot and stages every regular file under imgPrefix
// ("" or "/" for the image root). Symlinks are an error: FAT32 cannot
// represent them, and silently skipping would corrupt media.
func (m StageMap) AddTree(hostRoot, imgPrefix string) error {
	hostRoot = filepath.Clean(hostRoot)
	return filepath.WalkDir(hostRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fsimg: %s is a symlink; FAT32 media cannot contain symlinks", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("fsimg: %s is not a regular file", p)
		}
		rel, err := filepath.Rel(hostRoot, p)
		if err != nil {
			return err
		}
		m.AddFile(p, path.Join("/", imgPrefix, filepath.ToSlash(rel)))
		return nil
	})
}

// AddFile stages one host file at imgPath (normalized to a leading "/").
func (m StageMap) AddFile(hostPath, imgPath string) {
	m[path.Join("/", imgPath)] = hostPath
}

// Stats returns total content bytes and entry count (files + directories)
// for sizing. It stats every source file.
func (m StageMap) Stats() (bytes int64, entries int, err error) {
	dirs := map[string]bool{}
	for imgPath, hostPath := range m {
		st, err := os.Stat(hostPath)
		if err != nil {
			return 0, 0, err
		}
		bytes += st.Size()
		entries++
		for d := path.Dir(imgPath); d != "/" && !dirs[d]; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	return bytes, entries + len(dirs), nil
}

// Populate writes the staged files into fsys in sorted path order, creating
// directories as needed. Any file at or over the FAT32 4 GiB limit is an
// error naming the file (the Windows pipeline must have split such WIMs).
func Populate(fsys FS, m StageMap, progress func(done, total int64)) error {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var total int64
	for _, p := range paths {
		st, err := os.Stat(m[p])
		if err != nil {
			return err
		}
		if st.Size() > MaxFileSize {
			return fmt.Errorf("fsimg: %s is %d bytes, over the FAT32 4 GiB file limit (split WIMs with wimsplit/DISM)", m[p], st.Size())
		}
		total += st.Size()
	}

	for _, p := range paths {
		if err := validateImgPath(p); err != nil {
			return err
		}
	}

	madeDirs := map[string]bool{"/": true}
	buf := make([]byte, 1<<20)
	var done int64
	for _, p := range paths {
		dir := path.Dir(p)
		if !madeDirs[dir] {
			if err := fsys.Mkdir(fsPath(dir)); err != nil { // creates parents
				return fmt.Errorf("fsimg: mkdir %s: %w", dir, err)
			}
			for d := dir; d != "/"; d = path.Dir(d) {
				madeDirs[d] = true
			}
		}
		if err := copyFileInto(fsys, m[p], p, buf); err != nil {
			return err
		}
		if progress != nil {
			if st, err := os.Stat(m[p]); err == nil {
				done += st.Size()
			}
			progress(done, total)
		}
	}
	return nil
}

// fsPath converts a canonical image path ("/a/b", "/" for root) to the
// io/fs-style unrooted form go-diskfs expects ("a/b", "." for root); its
// ReadDir rejects rooted paths with iofs.ErrInvalid.
func fsPath(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" {
		return "."
	}
	return p
}

// validateImgPath enforces the v1 media character set: printable ASCII minus
// the characters FAT32 forbids. Non-ASCII names are rejected because the
// FAT32 backend's long-file-name lookup mishandles them (found by the acid
// test), and no supported install media needs them.
func validateImgPath(p string) error {
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("fsimg: invalid path component %q in %s", part, p)
		}
		if strings.TrimRight(part, ". ") != part {
			return fmt.Errorf("fsimg: name %q in %s may not end with a dot or space", part, p)
		}
		for _, r := range part {
			if r < 0x20 || r > 0x7e {
				return fmt.Errorf("fsimg: name %q in %s contains non-ASCII or control characters, unsupported on composed media (rename the source file)", part, p)
			}
			if strings.ContainsRune(`<>:"|?*\`, r) {
				return fmt.Errorf("fsimg: name %q in %s contains character %q, invalid on FAT32", part, p, r)
			}
		}
	}
	return nil
}

func copyFileInto(fsys FS, hostPath, imgPath string, buf []byte) error {
	src, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := fsys.OpenFile(fsPath(imgPath), os.O_CREATE|os.O_RDWR|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("fsimg: creating %s: %w", imgPath, err)
	}
	if _, err := io.CopyBuffer(dst, src, buf); err != nil {
		return fmt.Errorf("fsimg: writing %s: %w", imgPath, err)
	}
	if c, ok := dst.(io.Closer); ok {
		if err := c.Close(); err != nil {
			return fmt.Errorf("fsimg: closing %s: %w", imgPath, err)
		}
	}
	return nil
}

// ReadTreeSizes opens an existing image and returns imgPath → file size for
// every file on partition 1, for verification. Paths use forward slashes with
// a leading "/".
func ReadTreeSizes(imgPath string) (map[string]int64, error) {
	d, err := diskfs.Open(imgPath, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		return nil, fmt.Errorf("fsimg: opening %s: %w", imgPath, err)
	}
	defer d.Close()
	fsys, err := d.GetFilesystem(1)
	if err != nil {
		return nil, fmt.Errorf("fsimg: reading filesystem on partition 1 of %s: %w", imgPath, err)
	}
	sizes := map[string]int64{}
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := fsys.ReadDir(fsPath(dir))
		if err != nil {
			return fmt.Errorf("fsimg: reading directory %s: %w", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if name == "." || name == ".." {
				continue
			}
			full := path.Join(dir, name)
			if e.IsDir() {
				if err := walk(full); err != nil {
					return err
				}
				continue
			}
			info, err := e.Info()
			if err != nil {
				return err
			}
			sizes[full] = info.Size()
		}
		return nil
	}
	if err := walk("/"); err != nil {
		return nil, err
	}
	return sizes, nil
}

// OpenImageFile opens one file from partition 1 of an existing image for
// reading (verification support).
func OpenImageFile(imgPath, filePath string) (io.ReadCloser, func() error, error) {
	d, err := diskfs.Open(imgPath, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		return nil, nil, err
	}
	fsys, err := d.GetFilesystem(1)
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	f, err := fsys.OpenFile(fsPath(filePath), os.O_RDONLY)
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	return readCloser{f}, d.Close, nil
}

type readCloser struct{ f filesystem.File }

func (r readCloser) Read(p []byte) (int, error) { return r.f.Read(p) }
func (r readCloser) Close() error {
	if c, ok := r.f.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
