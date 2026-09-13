package flash

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/device"
)

// memTarget is a disk in memory. Cloning is the one operation here whose
// mistakes are measured in somebody else's data, and whose honest test on real
// hardware costs a drive every run.
type memTarget struct {
	buf []byte
	// failWriteAt makes writes fail from this offset on, standing in for a
	// stick pulled out halfway.
	failWriteAt int64
	// rot silently corrupts what is stored, standing in for a counterfeit
	// drive that accepts every write and keeps none of it.
	rot    bool
	closed bool
}

func (m *memTarget) Size() (int64, error) { return int64(len(m.buf)), nil }
func (m *memTarget) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > int64(len(m.buf)) {
		return 0, fmt.Errorf("read past end")
	}
	copy(p, m.buf[off:])
	return len(p), nil
}
func (m *memTarget) WriteAt(p []byte, off int64) (int, error) {
	if m.failWriteAt > 0 && off >= m.failWriteAt {
		return 0, errors.New("device disappeared")
	}
	if off+int64(len(p)) > int64(len(m.buf)) {
		return 0, fmt.Errorf("write past end")
	}
	copy(m.buf[off:], p)
	if m.rot {
		m.buf[off] ^= 0xFF
	}
	return len(p), nil
}
func (m *memTarget) Sync() error     { return nil }
func (m *memTarget) Finalize() error { return nil }
func (m *memTarget) Close() error    { m.closed = true; return nil }

// mbrDisk builds a disk whose MBR declares one partition covering partMiB,
// with recognisable bytes throughout so a short or misaligned copy shows up.
func mbrDisk(totalMiB, partMiB int) []byte {
	b := make([]byte, totalMiB<<20)
	for i := range b {
		b[i] = byte(i / SectorSize % 251)
	}
	b[510], b[511] = 0x55, 0xAA
	e := b[446:462]
	e[4] = 0x0C // FAT32 LBA
	binary.LittleEndian.PutUint32(e[8:12], 2048)
	binary.LittleEndian.PutUint32(e[12:16], uint32(partMiB<<20/SectorSize)-2048)
	return b
}

// withDisks swaps the opener so Clone runs against memory.
func withDisks(t *testing.T, disks map[string]*memTarget) {
	t.Helper()
	prev := openTarget
	openTarget = func(_ context.Context, d device.Device) (Target, error) {
		m, ok := disks[d.ID]
		if !ok {
			return nil, fmt.Errorf("no such disk %s", d.ID)
		}
		return m, nil
	}
	t.Cleanup(func() { openTarget = prev })
}

func usbDev(id string, mib int) device.Device {
	return device.Device{ID: id, Bus: "usb", Removable: true, SizeBytes: int64(mib) << 20}
}

// TestCloneOntoManyAtOnce is the mass-duplication case: every copy holds the
// source bytes, and every copy is identical.
func TestCloneOntoManyAtOnce(t *testing.T) {
	src := mbrDisk(32, 16)
	disks := map[string]*memTarget{
		"src": {buf: src},
		"a":   {buf: make([]byte, 32<<20)},
		"b":   {buf: make([]byte, 32<<20)},
		"c":   {buf: make([]byte, 32<<20)},
	}
	withDisks(t, disks)

	res, err := Clone(context.Background(), usbDev("src", 32),
		[]device.Device{usbDev("a", 32), usbDev("b", 32), usbDev("c", 32)}, false, nil)
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("got %d results, want 3", len(res))
	}
	for _, name := range []string{"a", "b", "c"} {
		if !equalBytes(disks[name].buf[:16<<20], src[:16<<20]) {
			t.Errorf("%s does not hold the source bytes", name)
		}
	}
	for i, r := range res {
		if r.Err != nil {
			t.Errorf("result %d: %v", i, r.Err)
		}
		if r.SHA256 == "" {
			t.Errorf("result %d was not verified", i)
		}
	}
}

// TestCopiesOnlyThroughTheLastPartition: a 1 TB drive holding 80 GB is an 80 GB
// copy. Taking the disk size literally is what makes a same-size clone fail
// when two drives differ by a few sectors, and it is hours of copying nothing.
func TestCopiesOnlyThroughTheLastPartition(t *testing.T) {
	disks := map[string]*memTarget{
		"src": {buf: mbrDisk(64, 8)},
		"dst": {buf: make([]byte, 64<<20)},
	}
	withDisks(t, disks)

	res, err := Clone(context.Background(), usbDev("src", 64),
		[]device.Device{usbDev("dst", 64)}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := res[0].Bytes; got > 12<<20 {
		t.Errorf("copied %d MiB; the partition ends at 8 MiB", got>>20)
	}
	// The tail past the partition must be left alone, not zero-filled.
	for _, b := range disks["dst"].buf[16<<20:] {
		if b != 0 {
			t.Fatal("wrote past the partition into the tail")
		}
	}
}

// TestSingleDestinationIsStillVerified is the bug this file was written after:
// comparing copies to each other leaves the ordinary case — one destination —
// checked against nothing.
func TestSingleDestinationIsStillVerified(t *testing.T) {
	disks := map[string]*memTarget{
		"src": {buf: mbrDisk(16, 8)},
		"dst": {buf: make([]byte, 16<<20), rot: true},
	}
	withDisks(t, disks)

	res, err := Clone(context.Background(), usbDev("src", 16),
		[]device.Device{usbDev("dst", 16)}, false, nil)
	if err == nil {
		t.Fatal("a drive that stored different bytes was reported as a success")
	}
	if len(res) == 1 && res[0].Err == nil {
		t.Error("the failing destination was not marked failed")
	}
	if !strings.Contains(err.Error(), "not storing what it accepted") {
		t.Errorf("unhelpful error for a lying drive: %v", err)
	}
}

// TestOneBadStickDoesNotCostTheBatch: nineteen good copies out of twenty is the
// right outcome, and each result says which one failed.
func TestOneBadStickDoesNotCostTheBatch(t *testing.T) {
	disks := map[string]*memTarget{
		"src":  {buf: mbrDisk(32, 16)},
		"good": {buf: make([]byte, 32<<20)},
		"bad":  {buf: make([]byte, 32<<20), failWriteAt: 4 << 20},
	}
	withDisks(t, disks)

	res, _ := Clone(context.Background(), usbDev("src", 32),
		[]device.Device{usbDev("good", 32), usbDev("bad", 32)}, false, nil)

	byID := map[string]CloneResult{}
	for _, r := range res {
		byID[r.Device.ID] = r
	}
	if byID["good"].Err != nil {
		t.Errorf("the healthy stick was failed by its neighbour: %v", byID["good"].Err)
	}
	if byID["bad"].Err == nil {
		t.Error("the stick that stopped accepting writes was reported as fine")
	}
	if !equalBytes(disks["good"].buf[:16<<20], disks["src"].buf[:16<<20]) {
		t.Error("the healthy stick did not receive the whole source")
	}
}

// TestRefusesTheCombinationsThatAreMistakes.
func TestRefusesTheCombinationsThatAreMistakes(t *testing.T) {
	disks := map[string]*memTarget{
		"src": {buf: mbrDisk(32, 16)},
		"dst": {buf: make([]byte, 32<<20)},
		"sml": {buf: make([]byte, 4<<20)},
	}
	withDisks(t, disks)
	ctx := context.Background()
	src := usbDev("src", 32)

	cases := []struct {
		name string
		dsts []device.Device
		want string
	}{
		{"source as its own destination", []device.Device{src}, "both the source and a destination"},
		{"same destination twice", []device.Device{usbDev("dst", 32), usbDev("dst", 32)}, "twice"},
		{"destination too small", []device.Device{usbDev("sml", 4)}, "needs"},
		{"no destination", nil, "no destination"},
		{"the system disk", []device.Device{{ID: "dst", Bus: "usb", System: true, SizeBytes: 32 << 20}}, "running OS"},
		{"a fixed disk without permission", []device.Device{{ID: "dst", Bus: "nvme", SizeBytes: 32 << 20}}, "fixed disk"},
	}
	for _, c := range cases {
		_, err := Clone(ctx, src, c.dsts, false, nil)
		if err == nil {
			t.Errorf("%s: allowed", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
}

// TestAFixedDiskIsReachableWithPermission — the point of the whole policy
// change. Same disk, same call, one extra argument.
func TestAFixedDiskIsReachableWithPermission(t *testing.T) {
	disks := map[string]*memTarget{
		"src": {buf: mbrDisk(32, 16)},
		"ssd": {buf: make([]byte, 32<<20)},
	}
	withDisks(t, disks)
	ssd := device.Device{ID: "ssd", Bus: "nvme", SizeBytes: 32 << 20}

	if _, err := Clone(context.Background(), usbDev("src", 32), []device.Device{ssd}, false, nil); err == nil {
		t.Fatal("wrote to a fixed disk without permission")
	}
	if _, err := Clone(context.Background(), usbDev("src", 32), []device.Device{ssd}, true, nil); err != nil {
		t.Fatalf("permission was given and it still refused: %v", err)
	}
	if !equalBytes(disks["ssd"].buf[:16<<20], disks["src"].buf[:16<<20]) {
		t.Error("the fixed disk was not written")
	}
}

// TestTheSystemDiskCanBeTheSource: reading it is how somebody backs up the
// machine they are sitting at, and reading harms nothing.
func TestTheSystemDiskCanBeTheSource(t *testing.T) {
	disks := map[string]*memTarget{
		"sys": {buf: mbrDisk(32, 16)},
		"dst": {buf: make([]byte, 32<<20)},
	}
	withDisks(t, disks)
	sys := device.Device{ID: "sys", Bus: "nvme", System: true, SizeBytes: 32 << 20}

	if _, err := Clone(context.Background(), sys, []device.Device{usbDev("dst", 32)}, false, nil); err != nil {
		t.Fatalf("refused to read the system disk: %v", err)
	}
	if !equalBytes(disks["dst"].buf[:16<<20], disks["sys"].buf[:16<<20]) {
		t.Error("the backup does not hold the system disk's bytes")
	}
}

// TestProgressNamesItsDisk: twenty sticks writing at once is unreadable unless
// each line says which stick it belongs to.
func TestProgressNamesItsDisk(t *testing.T) {
	disks := map[string]*memTarget{
		"src": {buf: mbrDisk(32, 16)},
		"a":   {buf: make([]byte, 32<<20)},
		"b":   {buf: make([]byte, 32<<20)},
	}
	withDisks(t, disks)

	seen := map[string]map[string]bool{}
	_, err := Clone(context.Background(), usbDev("src", 32),
		[]device.Device{usbDev("a", 32), usbDev("b", 32)}, false,
		func(target, stage string, done, total int64) {
			if seen[target] == nil {
				seen[target] = map[string]bool{}
			}
			seen[target][stage] = true
		})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		for _, stage := range []string{"copy", "verify"} {
			if !seen[id][stage] {
				t.Errorf("no %q progress reported for %s", stage, id)
			}
		}
	}
}
