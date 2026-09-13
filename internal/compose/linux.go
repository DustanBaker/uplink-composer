package compose

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition/gpt"

	"github.com/uplinkresearch/bootwright/internal/library"
	"github.com/uplinkresearch/bootwright/internal/recipe"
	"github.com/uplinkresearch/bootwright/internal/stream"
)

const (
	// cidataSize is the appended NoCloud partition: FAT16 needs ≥ 4085
	// clusters, and 8 MiB leaves room for user-data that embeds scripts
	// or certificates.
	cidataSize = 8 << 20
	// gptLinuxFilesystem is the GPT type GUID for a Linux data partition.
	gptLinuxFilesystem = gpt.Type("0FC63DAF-8483-4772-8E79-3D69D8477DE4")
	grubCfgPath        = "/boot/grub/grub.cfg"
	sectorSize         = 512
)

// buildLinuxAutoinstall turns a hybrid Ubuntu ISO into zero-touch install
// media: the ISO bytes copied into an image, GRUB rewritten in place (same
// byte length — no ISO rebuild) to boot with `autoinstall`, and a CIDATA
// partition appended with the rendered cloud-init user-data/meta-data.
func buildLinuxAutoinstall(ctx context.Context, req Request, entry library.Entry, blob, comp string) (*Artifact, error) {
	r := req.Recipe
	ws := req.Workspace
	lib := req.Library
	a := r.Linux.Autoinstall

	key, err := inputsKey(req, entry.SHA256)
	if err != nil {
		return nil, err
	}
	imgPath := filepath.Join(lib.ArtifactsDir(), fmt.Sprintf("%s-%s.img", r.ID, key))
	if !req.Rebuild {
		if art, err := LoadArtifact(MetaPath(imgPath)); err == nil {
			if _, err := os.Stat(art.Path); err == nil {
				req.progress("cached", 1, 1)
				return art, nil
			}
		}
	}

	// ── Render cloud-init data first: cheap, and it fails fast on template
	// mistakes before the multi-GB copy. ──────────────────────────────────
	vars, err := overlayVars(ws.MergedVars(r, req.CLIVars), a.Vars)
	if err != nil {
		return nil, fmt.Errorf("compose: autoinstall vars: %w", err)
	}
	tctx := recipe.Context{Org: ws.Org(), Vars: vars, Recipe: r}
	userData, err := recipe.RenderTemplate(filepath.Join(ws.Dir, filepath.FromSlash(a.UserData)), tctx)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.TrimSpace(userData), "#cloud-config") {
		return nil, fmt.Errorf("compose: %s must start with #cloud-config (cloud-init ignores it otherwise)", a.UserData)
	}
	metaData := fmt.Sprintf("instance-id: bootwright-%s-%s\n", r.ID, key)
	if a.MetaData != "" {
		if metaData, err = recipe.RenderTemplate(filepath.Join(ws.Dir, filepath.FromSlash(a.MetaData)), tctx); err != nil {
			return nil, err
		}
	}

	// ── Materialize the ISO bytes ────────────────────────────────────────
	if err := copyBlobToImage(ctx, req, blob, comp, imgPath, entry.Size); err != nil {
		return nil, err
	}
	cleanup := func() { os.Remove(imgPath) }

	// ── GRUB rewrite for zero-touch boot ─────────────────────────────────
	if a.PatchKernel() {
		req.progress("patch grub", 0, -1)
		if err := patchGrubForAutoinstall(imgPath); err != nil {
			cleanup()
			return nil, err
		}
	}

	// ── CIDATA partition ─────────────────────────────────────────────────
	req.progress("cidata", 0, -1)
	if err := appendCIDATA(imgPath, userData, metaData); err != nil {
		cleanup()
		return nil, err
	}

	req.progress("hash", 0, -1)
	sum, size, err := hashFileWithProgress(imgPath, func(done, total int64) { req.progress("hash", done, total) })
	if err != nil {
		cleanup()
		return nil, err
	}
	art := &Artifact{
		RecipeID: r.ID, Kind: "image", Path: imgPath, Size: size, SHA256: sum,
		InputsKey: key, Verify: r.Flash.Verify, MinStick: minStickBytes(r),
		CreatedAt: nowUTC(), Tool: toolVersion(),
	}
	return art, art.save()
}

// copyBlobToImage streams (decompressing as needed) the ISO into imgPath,
// padded to a whole sector.
func copyBlobToImage(ctx context.Context, req Request, blob, comp, imgPath string, hint int64) error {
	in, err := stream.Open(blob, comp)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(imgPath)
	if err != nil {
		return err
	}
	total := hint
	if comp != "" && comp != "none" {
		total = -1
	}
	buf := make([]byte, 4<<20)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			os.Remove(imgPath)
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				os.Remove(imgPath)
				return werr
			}
			done += int64(n)
			req.progress("copy iso", done, total)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(imgPath)
			return rerr
		}
	}
	if pad := (sectorSize - done%sectorSize) % sectorSize; pad != 0 {
		if _, err := out.Write(make([]byte, pad)); err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}

var (
	grubKernelRe = regexp.MustCompile(`(?m)^\s*linux\s+(\S+)`)
	grubInitrdRe = regexp.MustCompile(`(?m)^\s*initrd\s+(\S+)`)
)

// grubAutoinstallMenu builds a replacement grub.cfg of exactly len(orig)
// bytes: one entry booting the ISO's own kernel/initrd with `autoinstall`.
func grubAutoinstallMenu(orig []byte) ([]byte, error) {
	k := grubKernelRe.FindSubmatch(orig)
	i := grubInitrdRe.FindSubmatch(orig)
	if k == nil || i == nil {
		return nil, fmt.Errorf("compose: could not find linux/initrd lines in the ISO's grub.cfg")
	}
	menu := fmt.Sprintf("set timeout=2\nmenuentry \"Automated install\" {\n\tset gfxpayload=keep\n\tlinux\t%s autoinstall ---\n\tinitrd\t%s\n}\n", k[1], i[1])
	if len(menu) > len(orig) {
		return nil, fmt.Errorf("compose: replacement grub.cfg (%d bytes) exceeds the original (%d) — cannot patch in place", len(menu), len(orig))
	}
	out := make([]byte, len(orig))
	copy(out, menu)
	for j := len(menu); j < len(out); j++ {
		out[j] = '\n'
	}
	return out, nil
}

// patchGrubForAutoinstall locates the ISO's /boot/grub/grub.cfg bytes inside
// the image and overwrites them with a same-length autoinstall menu.
func patchGrubForAutoinstall(imgPath string) error {
	orig, err := readISOFile(imgPath, grubCfgPath)
	if err != nil {
		return fmt.Errorf("compose: reading %s from the ISO: %w", grubCfgPath, err)
	}
	if len(orig) < 32 {
		return fmt.Errorf("compose: %s is implausibly small (%d bytes)", grubCfgPath, len(orig))
	}
	repl, err := grubAutoinstallMenu(orig)
	if err != nil {
		return err
	}
	off, err := findBytes(imgPath, orig)
	if err != nil {
		return err
	}
	if off < 0 {
		return fmt.Errorf("compose: %s content not found in the image", grubCfgPath)
	}
	f, err := os.OpenFile(imgPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteAt(repl, off); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// Prove the rewrite is visible through the filesystem, not just the raw bytes.
	check, err := readISOFile(imgPath, grubCfgPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(check, repl) {
		return fmt.Errorf("compose: grub.cfg rewrite did not read back through ISO9660")
	}
	return nil
}

// readISOFile reads one file from the ISO9660 filesystem at image offset 0
// (the hybrid layout keeps the ISO at the start regardless of GPT).
func readISOFile(imgPath, p string) ([]byte, error) {
	// ISO9660 is addressed in 2048-byte blocks; the GPT (512-byte sectors)
	// is irrelevant for this read and its parse failure is ignored.
	d, err := diskfs.Open(imgPath, diskfs.WithOpenMode(diskfs.ReadOnly), diskfs.WithSectorSize(2048))
	if err != nil {
		return nil, err
	}
	defer d.Close()
	fsys, err := d.GetFilesystem(0)
	if err != nil {
		return nil, err
	}
	var f filesystem.File
	for _, candidate := range []string{p, strings.TrimPrefix(p, "/")} {
		if f, err = fsys.OpenFile(candidate, os.O_RDONLY); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if c, ok := f.(io.Closer); ok {
		c.Close()
	}
	return b, err
}

// findBytes returns the offset of needle in the file, or -1.
func findBytes(path string, needle []byte) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	const chunk = 8 << 20
	buf := make([]byte, chunk+len(needle))
	var base int64
	carry := 0
	for {
		n, err := f.Read(buf[carry:])
		if n == 0 && err == io.EOF {
			return -1, nil
		}
		if err != nil && err != io.EOF {
			return -1, err
		}
		view := buf[:carry+n]
		if i := bytes.Index(view, needle); i >= 0 {
			return base + int64(i), nil
		}
		// Keep a needle-length tail so matches spanning chunks are found.
		keep := len(needle) - 1
		if keep > len(view) {
			keep = len(view)
		}
		copy(buf, view[len(view)-keep:])
		base += int64(len(view) - keep)
		carry = keep
		if err == io.EOF {
			return -1, nil
		}
	}
}

// appendCIDATA grows the image by a FAT16 partition labeled CIDATA holding
// user-data and meta-data — cloud-init's NoCloud datasource. The isohybrid
// MBR (sector 0) is never touched; the GPT is resized so its backup header
// lands at the new end of the image.
func appendCIDATA(imgPath, userData, metaData string) error {
	st, err := os.Stat(imgPath)
	if err != nil {
		return err
	}
	const align = 1 << 20
	start := (st.Size() + align - 1) / align * align
	newSize := (start + cidataSize + 34*sectorSize + align - 1) / align * align
	if err := os.Truncate(imgPath, newSize); err != nil {
		return err
	}

	d, err := diskfs.Open(imgPath, diskfs.WithOpenMode(diskfs.ReadWrite))
	if err != nil {
		return err
	}
	defer d.Close()
	tbl, err := d.GetPartitionTable()
	if err != nil {
		return fmt.Errorf("compose: the ISO has no partition table — autoinstall needs a hybrid (GPT) ISO: %w", err)
	}
	gt, ok := tbl.(*gpt.Table)
	if !ok {
		return fmt.Errorf("compose: the ISO's partition table is not GPT — autoinstall needs a hybrid (GPT) ISO")
	}
	gt.Resize(uint64(newSize))
	gt.ProtectiveMBR = false
	startLBA := uint64(start / sectorSize)
	endLBA := startLBA + cidataSize/sectorSize - 1
	idx := len(gt.Partitions) + 1
	gt.Partitions = append(gt.Partitions, &gpt.Partition{
		Index: idx,
		Start: startLBA,
		End:   endLBA,
		Size:  cidataSize,
		Type:  gptLinuxFilesystem,
		Name:  "CIDATA",
	})
	if err := d.Partition(gt); err != nil {
		return fmt.Errorf("compose: appending CIDATA partition: %w", err)
	}
	fsys, err := d.CreateFilesystem(disk.FilesystemSpec{
		Partition:   idx,
		FSType:      filesystem.TypeFat16,
		VolumeLabel: "CIDATA",
	})
	if err != nil {
		return fmt.Errorf("compose: formatting CIDATA: %w", err)
	}
	for name, content := range map[string]string{"user-data": userData, "meta-data": metaData} {
		f, err := fsys.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_TRUNC)
		if err != nil {
			return fmt.Errorf("compose: writing %s: %w", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			return err
		}
		if c, ok := f.(io.Closer); ok {
			c.Close()
		}
	}
	return nil
}
