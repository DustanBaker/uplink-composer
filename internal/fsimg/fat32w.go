package fsimg

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
)

// BuildStaged writes a complete FAT32 image from a StageMap in one pass.
//
// go-diskfs formats and fills a FAT32 volume one write at a time, and every
// write that grows a file rewrites the entire allocation table: a Windows 11
// image (8 GiB, a 9 MiB table) spent 73% of its time serialising the table and
// took about 35 minutes. Every file is known before the image is written, so
// this lays the volume out up front instead — directories first, then each
// file in one contiguous run of clusters — writes both FATs once, and streams
// file contents straight to their final offsets. go-diskfs still writes the
// partition table and is what the tests read images back with.
//
// Written from Microsoft's FAT specification (fatgen103): FAT32 only, 512-byte
// sectors, the default cluster sizes by volume size, two FATs, FSInfo and a
// backup boot sector, long file names with their checksums, and ".." set to
// cluster 0 for a directory whose parent is the root.
func BuildStaged(imgPath string, opts Options, stage StageMap, progress func(done, total int64)) (err error) {
	switch {
	case opts.SizeBytes <= 0 || opts.SizeBytes%SectorSize != 0:
		return fmt.Errorf("fsimg: SizeBytes %d must be a positive multiple of %d", opts.SizeBytes, SectorSize)
	case opts.Scheme != SchemeMBR && opts.Scheme != SchemeGPT:
		return fmt.Errorf("fsimg: unknown partition scheme %q", opts.Scheme)
	}
	paths := make([]string, 0, len(stage))
	sizes := map[string]int64{}
	var total int64
	for p, host := range stage {
		if err := validateImgPath(p); err != nil {
			return err
		}
		st, err := os.Stat(host)
		if err != nil {
			return err
		}
		if st.Size() > MaxFileSize {
			return fmt.Errorf("fsimg: %s is %d bytes, over the FAT32 4 GiB file limit (split WIMs with wimsplit/DISM)", host, st.Size())
		}
		paths = append(paths, p)
		sizes[p] = st.Size()
		total += st.Size()
	}
	sort.Strings(paths)

	// ── Partition table, via go-diskfs ───────────────────────────────────
	if err := os.Remove(imgPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fsimg: replacing %s: %w", imgPath, err)
	}
	d, err := diskfs.Create(imgPath, opts.SizeBytes, diskfs.SectorSizeDefault)
	if err != nil {
		return fmt.Errorf("fsimg: creating image: %w", err)
	}
	table, partSectors := partitionTable(opts)
	if err := d.Partition(table); err != nil {
		d.Close()
		os.Remove(imgPath)
		return fmt.Errorf("fsimg: writing partition table: %w", err)
	}
	if err := d.Close(); err != nil {
		os.Remove(imgPath)
		return err
	}
	f, err := os.OpenFile(imgPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(imgPath)
		}
	}()

	g, err := newGeometry(partSectors)
	if err != nil {
		return err
	}
	base := int64(partStartSector) * SectorSize
	stamp := imageTime(opts.Reproducible)

	// ── Tree and layout ──────────────────────────────────────────────────
	root := &fatDir{path: "/"}
	dirs := map[string]*fatDir{"/": root}
	var ensure func(p string) *fatDir
	ensure = func(p string) *fatDir {
		if dd, ok := dirs[p]; ok {
			return dd
		}
		parent := ensure(path.Dir(p))
		dd := &fatDir{path: p, name: path.Base(p), parent: parent}
		parent.subdirs = append(parent.subdirs, dd)
		dirs[p] = dd
		return dd
	}
	type fatFile struct {
		path    string
		size    int64
		cluster uint32
	}
	var files []*fatFile
	for _, p := range paths {
		parent := ensure(path.Dir(p))
		ff := &fatFile{path: p, size: sizes[p]}
		parent.files = append(parent.files, ff.path)
		files = append(files, ff)
	}

	fat := make([]uint32, g.clusters+2)
	fat[0], fat[1] = 0x0FFFFFF8, 0x0FFFFFFF
	next := uint32(2)
	alloc := func(bytes int64) (uint32, error) {
		n := uint32((bytes + g.clusterBytes - 1) / g.clusterBytes)
		if n == 0 {
			return 0, nil
		}
		if int64(next)+int64(n) > int64(g.clusters)+2 {
			return 0, fmt.Errorf("fsimg: the content does not fit in a %d MiB image", opts.SizeBytes>>20)
		}
		start := next
		for c := start; c < start+n-1; c++ {
			fat[c] = c + 1
		}
		fat[start+n-1] = 0x0FFFFFFF
		next += n
		return start, nil
	}

	// Directories first, breadth first, so the volume's metadata sits
	// together at its start. Entry counts are known before contents are, so
	// sizes come first and clusters are fixed before any entry is written.
	order := []*fatDir{root}
	for i := 0; i < len(order); i++ {
		sort.Slice(order[i].subdirs, func(a, b int) bool { return order[i].subdirs[a].name < order[i].subdirs[b].name })
		order = append(order, order[i].subdirs...)
	}
	for _, dd := range order {
		dd.entries = dd.buildEntries(dirs, sizes, opts.Label)
		c, err := alloc(max(int64(len(dd.entries)), 1) * 32)
		if err != nil {
			return err
		}
		dd.cluster = c
	}
	fileCluster := map[string]uint32{}
	for _, ff := range files {
		c, err := alloc(ff.size)
		if err != nil {
			return err
		}
		ff.cluster = c
		fileCluster[ff.path] = c
	}

	// ── Boot sector, FSInfo, backups ─────────────────────────────────────
	volID := uint32(stamp.Unix())
	if opts.Reproducible {
		volID = crc32.ChecksumIEEE([]byte(fmt.Sprintf("%s|%d", opts.Label, opts.SizeBytes)))
	}
	boot := g.bootSector(partStartSector, volID, opts.Label)
	info := make([]byte, SectorSize)
	binary.LittleEndian.PutUint32(info[0:], 0x41615252)
	binary.LittleEndian.PutUint32(info[484:], 0x61417272)
	binary.LittleEndian.PutUint32(info[488:], g.clusters-(next-2))
	binary.LittleEndian.PutUint32(info[492:], next)
	binary.LittleEndian.PutUint32(info[508:], 0xAA550000)
	for _, w := range []struct {
		sector int64
		data   []byte
	}{{0, boot}, {1, info}, {6, boot}, {7, info}} {
		if _, err := f.WriteAt(w.data, base+w.sector*SectorSize); err != nil {
			return err
		}
	}

	// ── Both FATs ────────────────────────────────────────────────────────
	fatBytes := make([]byte, len(fat)*4)
	for i, v := range fat {
		binary.LittleEndian.PutUint32(fatBytes[i*4:], v)
	}
	for i := int64(0); i < 2; i++ {
		if _, err := f.WriteAt(fatBytes, base+(g.reserved+i*g.fatSectors)*SectorSize); err != nil {
			return err
		}
	}

	// ── Directories ──────────────────────────────────────────────────────
	clusterOffset := func(c uint32) int64 { return base + g.dataStart*SectorSize + int64(c-2)*g.clusterBytes }
	for _, dd := range order {
		buf := make([]byte, 0, len(dd.entries)*32)
		for _, e := range dd.entries {
			buf = append(buf, e.bytes(dirs, fileCluster, stamp)...)
		}
		if _, err := f.WriteAt(buf, clusterOffset(dd.cluster)); err != nil {
			return err
		}
	}

	// ── File contents, in allocation order ───────────────────────────────
	copyBuf := make([]byte, 8<<20)
	var done int64
	for _, ff := range files {
		if ff.size == 0 {
			continue
		}
		src, err := os.Open(stage[ff.path])
		if err != nil {
			return err
		}
		n, err := io.CopyBuffer(io.NewOffsetWriter(f, clusterOffset(ff.cluster)), io.LimitReader(src, ff.size), copyBuf)
		src.Close()
		if err != nil {
			return fmt.Errorf("fsimg: writing %s: %w", ff.path, err)
		}
		if n != ff.size {
			return fmt.Errorf("fsimg: %s changed size while being written", ff.path)
		}
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	return f.Sync()
}

// geometry is a FAT32 volume's layout, per fatgen103.
type geometry struct {
	sectors      int64
	secPerClus   int64
	reserved     int64
	fatSectors   int64
	dataStart    int64 // in sectors from the start of the volume
	clusters     uint32
	clusterBytes int64
}

func newGeometry(sectors int64) (geometry, error) {
	g := geometry{sectors: sectors, reserved: 32}
	// Microsoft's default cluster sizes for FAT32 by volume size.
	switch {
	case sectors <= 532480:
		g.secPerClus = 1
	case sectors <= 16777216:
		g.secPerClus = 8
	case sectors <= 33554432:
		g.secPerClus = 16
	case sectors <= 67108864:
		g.secPerClus = 32
	default:
		g.secPerClus = 64
	}
	tmp1 := sectors - g.reserved
	tmp2 := (256*g.secPerClus + 2) / 2
	g.fatSectors = (tmp1 + tmp2 - 1) / tmp2
	g.dataStart = g.reserved + 2*g.fatSectors
	g.clusterBytes = g.secPerClus * SectorSize
	count := (sectors - g.dataStart) / g.secPerClus
	if count < 65525 {
		return g, fmt.Errorf("fsimg: a %d MiB volume is too small for FAT32", sectors*SectorSize>>20)
	}
	if count > 0x0FFFFFF5 {
		return g, fmt.Errorf("fsimg: volume too large for FAT32")
	}
	g.clusters = uint32(count)
	return g, nil
}

func (g geometry) bootSector(hidden int64, volID uint32, label string) []byte {
	b := make([]byte, SectorSize)
	copy(b[0:], []byte{0xEB, 0x58, 0x90})
	copy(b[3:], "MSWIN4.1")
	binary.LittleEndian.PutUint16(b[11:], SectorSize)
	b[13] = byte(g.secPerClus)
	binary.LittleEndian.PutUint16(b[14:], uint16(g.reserved))
	b[16] = 2 // FATs
	b[21] = 0xF8
	binary.LittleEndian.PutUint16(b[24:], 63)
	binary.LittleEndian.PutUint16(b[26:], 255)
	binary.LittleEndian.PutUint32(b[28:], uint32(hidden))
	binary.LittleEndian.PutUint32(b[32:], uint32(g.sectors))
	binary.LittleEndian.PutUint32(b[36:], uint32(g.fatSectors))
	binary.LittleEndian.PutUint32(b[44:], 2) // root directory cluster
	binary.LittleEndian.PutUint16(b[48:], 1) // FSInfo
	binary.LittleEndian.PutUint16(b[50:], 6) // backup boot sector
	b[64] = 0x80
	b[66] = 0x29
	binary.LittleEndian.PutUint32(b[67:], volID)
	copy(b[71:82], volumeLabel(label))
	copy(b[82:], "FAT32   ")
	b[510], b[511] = 0x55, 0xAA
	return b
}

func volumeLabel(label string) []byte {
	l := []byte(strings.ToUpper(label))
	if len(l) > 11 {
		l = l[:11]
	}
	out := []byte("           ")
	copy(out, l)
	return out
}

// imageTime is the timestamp every entry carries: SOURCE_DATE_EPOCH for a
// reproducible image, otherwise now.
func imageTime(reproducible bool) time.Time {
	if reproducible {
		if v, err := strconv.ParseInt(os.Getenv("SOURCE_DATE_EPOCH"), 10, 64); err == nil {
			return time.Unix(v, 0).UTC()
		}
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Now()
}

func dosTime(t time.Time) (date, clock uint16) {
	y := t.Year() - 1980
	if y < 0 {
		return 0x21, 0 // 1980-01-01
	}
	if y > 127 {
		y = 127
	}
	date = uint16(y)<<9 | uint16(t.Month())<<5 | uint16(t.Day())
	clock = uint16(t.Hour())<<11 | uint16(t.Minute())<<5 | uint16(t.Second()/2)
	return date, clock
}

type fatDir struct {
	path    string
	name    string
	parent  *fatDir
	subdirs []*fatDir
	files   []string
	cluster uint32
	entries []dirEntry
}

// dirEntry is one 32-byte slot, filled in once clusters are known.
type dirEntry struct {
	kind   byte // 'l' label, '.' dot, 'p' dotdot, 'L' long-name slot, 'd' dir, 'f' file
	raw    [32]byte
	target string // the directory or file an 8.3 entry points at
	size   int64
}

func (dd *fatDir) buildEntries(dirs map[string]*fatDir, sizes map[string]int64, label string) []dirEntry {
	var out []dirEntry
	if dd.parent == nil {
		// The volume label is also an entry in the root directory, and fsck
		// rejects a blank one.
		if strings.TrimSpace(label) != "" {
			e := dirEntry{kind: 'l'}
			copy(e.raw[0:11], volumeLabel(label))
			out = append(out, e)
		}
	} else {
		out = append(out, dirEntry{kind: '.', target: dd.path}, dirEntry{kind: 'p', target: dd.parent.path})
	}
	type child struct {
		name, full string
		dir        bool
	}
	var children []child
	for _, s := range dd.subdirs {
		children = append(children, child{s.name, s.path, true})
	}
	for _, p := range dd.files {
		children = append(children, child{path.Base(p), p, false})
	}
	sort.Slice(children, func(a, b int) bool { return children[a].name < children[b].name })
	used := map[string]bool{}
	for _, c := range children {
		short, lower, needLong := shortName(c.name, used)
		used[string(short[:])] = true
		if needLong {
			out = append(out, longEntries(c.name, short)...)
		}
		e := dirEntry{kind: 'f', target: c.full}
		if c.dir {
			e.kind = 'd'
		} else {
			e.size = sizes[c.full]
		}
		copy(e.raw[0:11], short[:])
		e.raw[12] = lower
		out = append(out, e)
	}
	return out
}

func (e dirEntry) bytes(dirs map[string]*fatDir, fileCluster map[string]uint32, stamp time.Time) []byte {
	b := e.raw
	date, clock := dosTime(stamp)
	stampAll := func() {
		binary.LittleEndian.PutUint16(b[14:], clock)
		binary.LittleEndian.PutUint16(b[16:], date)
		binary.LittleEndian.PutUint16(b[18:], date)
		binary.LittleEndian.PutUint16(b[22:], clock)
		binary.LittleEndian.PutUint16(b[24:], date)
	}
	setCluster := func(c uint32) {
		binary.LittleEndian.PutUint16(b[20:], uint16(c>>16))
		binary.LittleEndian.PutUint16(b[26:], uint16(c))
	}
	switch e.kind {
	case 'l':
		b[11] = 0x08
		stampAll()
	case '.':
		copy(b[0:11], ".          ")
		b[11] = 0x10
		setCluster(dirs[e.target].cluster)
		stampAll()
	case 'p':
		copy(b[0:11], "..         ")
		b[11] = 0x10
		// A directory whose parent is the root points ".." at cluster 0.
		if dirs[e.target].parent != nil {
			setCluster(dirs[e.target].cluster)
		}
		stampAll()
	case 'd':
		b[11] = 0x10
		setCluster(dirs[e.target].cluster)
		stampAll()
	case 'f':
		b[11] = 0x20
		setCluster(fileCluster[e.target])
		binary.LittleEndian.PutUint32(b[28:], uint32(e.size))
		stampAll()
	}
	return b[:]
}

const shortChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789$%'-_@~`!(){}^#&"

// shortName returns the 8.3 name for a directory entry, the NT case flags
// for a name that differs from its 8.3 form only by being all lower case (how
// Windows stores "setup.exe" without a long name), and whether a long name is
// needed.
func shortName(name string, used map[string]bool) (short [11]byte, lowerFlags byte, needLong bool) {
	base, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		base, ext = name[:i], name[i+1:]
	}
	valid := func(s string, max int) bool {
		if s == "" && max == 8 || len(s) > max {
			return false
		}
		for _, r := range strings.ToUpper(s) {
			if !strings.ContainsRune(shortChars, r) {
				return false
			}
		}
		return true
	}
	caseFlag := func(s string, flag byte) (byte, bool) {
		switch s {
		case strings.ToUpper(s):
			return 0, true
		case strings.ToLower(s):
			return flag, true
		}
		return 0, false
	}
	if valid(base, 8) && valid(ext, 3) && !strings.Contains(base, ".") {
		bf, okB := caseFlag(base, 0x08)
		ef, okE := caseFlag(ext, 0x10)
		copy(short[:], fmt.Sprintf("%-8s%-3s", strings.ToUpper(base), strings.ToUpper(ext)))
		if okB && okE && !used[string(short[:])] {
			return short, bf | ef, false
		}
	}
	// A generated basis name with a numeric tail, as Windows makes: the
	// first characters that are legal in 8.3, then ~1, ~2…
	clean := func(s string, max int) string {
		var b strings.Builder
		for _, r := range strings.ToUpper(s) {
			if r == ' ' || r == '.' {
				continue
			}
			if !strings.ContainsRune(shortChars, r) {
				r = '_'
			}
			b.WriteRune(r)
			if b.Len() == max {
				break
			}
		}
		return b.String()
	}
	b8, e3 := clean(base, 8), clean(ext, 3)
	if b8 == "" {
		b8 = "_"
	}
	for n := 1; ; n++ {
		tail := "~" + strconv.Itoa(n)
		stem := b8
		if len(stem)+len(tail) > 8 {
			stem = stem[:8-len(tail)]
		}
		copy(short[:], fmt.Sprintf("%-8s%-3s", stem+tail, e3))
		if !used[string(short[:])] {
			return short, 0, true
		}
	}
}

// longEntries are the long-file-name slots that precede an 8.3 entry, last
// part first, each holding 13 UTF-16 characters and the 8.3 name's checksum.
func longEntries(name string, short [11]byte) []dirEntry {
	var sum byte
	for _, c := range short {
		sum = (sum>>1 | sum<<7) + c
	}
	chars := []uint16{}
	for _, r := range name {
		chars = append(chars, uint16(r)) // names are printable ASCII (validateImgPath)
	}
	if len(chars)%13 != 0 {
		chars = append(chars, 0)
		for len(chars)%13 != 0 {
			chars = append(chars, 0xFFFF)
		}
	}
	n := len(chars) / 13
	out := make([]dirEntry, 0, n)
	for i := n; i >= 1; i-- {
		var e dirEntry
		e.kind = 'L'
		ord := byte(i)
		if i == n {
			ord |= 0x40
		}
		e.raw[0] = ord
		e.raw[11] = 0x0F
		e.raw[13] = sum
		part := chars[(i-1)*13 : i*13]
		offsets := []int{1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30}
		for j, off := range offsets {
			binary.LittleEndian.PutUint16(e.raw[off:], part[j])
		}
		out = append(out, e)
	}
	return out
}
