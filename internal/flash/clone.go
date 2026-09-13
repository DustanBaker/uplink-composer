package flash

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/uplinkresearch/dsky/internal/device"
)

// CloneProgress reports one target's progress. target is the device ID, or ""
// for stages that belong to the whole job rather than one disk.
//
// It is called concurrently, once per destination per chunk, and must be safe
// for that. Twenty sticks writing at once means twenty goroutines calling this;
// a callback that appends to a slice without a lock will corrupt it.
//
// Within Clone itself the goroutines touch only their own result entry and are
// joined before anything reads them, so the results slice needs no lock.
type CloneProgress func(target, stage string, done, total int64)

// CloneResult is what one destination ended up with.
type CloneResult struct {
	Device device.Device
	Bytes  int64
	SHA256 string // of the bytes read back, when verified
	Err    error
}

// Clone copies one disk onto one or more others, reading the source once and
// writing every destination from the same bytes.
//
// Reading once is not only faster. A disk read twice can return different
// bytes — something writes to it between passes, a drive remaps a sector — and
// duplicating from one read means the copies are identical to each other even
// in that case, which is the property somebody duplicating twenty sticks
// actually wants.
//
// The source is read, never written, so it may be any disk at all, including
// the one the OS is running from. Destinations go through CheckTarget.
func Clone(ctx context.Context, src device.Device, dsts []device.Device, allowFixed bool, progress CloneProgress) ([]CloneResult, error) {
	if progress == nil {
		progress = func(string, string, int64, int64) {}
	}
	if len(dsts) == 0 {
		return nil, errors.New("clone: no destination")
	}
	if err := checkPairing(src, dsts, allowFixed); err != nil {
		return nil, err
	}

	in, err := openTarget(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("clone: opening source %s: %w", src.ID, err)
	}
	defer in.Close()

	extent, err := cloneExtent(in, src)
	if err != nil {
		return nil, err
	}
	for _, d := range dsts {
		if d.SizeBytes > 0 && d.SizeBytes < extent {
			return nil, fmt.Errorf("clone: %s holds %s but the copy needs %s",
				d.ID, gb(d.SizeBytes), gb(extent))
		}
	}

	outs := make([]Target, 0, len(dsts))
	defer func() {
		for _, o := range outs {
			o.Close()
		}
	}()
	for _, d := range dsts {
		progress(d.ID, "open", 0, extent)
		o, err := openTarget(ctx, d)
		if err != nil {
			return nil, fmt.Errorf("clone: opening %s: %w", d.ID, err)
		}
		outs = append(outs, o)
	}

	results := make([]CloneResult, len(dsts))
	for i := range dsts {
		results[i].Device = dsts[i]
	}

	srcSum, err := copyFanOut(ctx, in, outs, dsts, extent, results, progress)
	if err != nil {
		return results, err
	}
	for i, o := range outs {
		if results[i].Err != nil {
			continue
		}
		if err := o.Sync(); err != nil {
			results[i].Err = fmt.Errorf("flush: %w", err)
		}
	}
	verifyAll(ctx, outs, dsts, extent, srcSum, results, progress)
	for i, o := range outs {
		if results[i].Err == nil {
			if err := o.Finalize(); err != nil {
				results[i].Err = fmt.Errorf("finalize: %w", err)
			}
		}
	}
	return results, firstErr(results)
}

// checkPairing rejects the combinations that are mistakes rather than choices.
func checkPairing(src device.Device, dsts []device.Device, allowFixed bool) error {
	seen := map[string]bool{}
	for _, d := range dsts {
		if d.ID == src.ID {
			return fmt.Errorf("clone: %s is both the source and a destination", d.ID)
		}
		if seen[d.ID] {
			return fmt.Errorf("clone: %s is listed as a destination twice", d.ID)
		}
		seen[d.ID] = true
		if err := CheckTarget(d, allowFixed); err != nil {
			return fmt.Errorf("clone: %w", err)
		}
	}
	return nil
}

// cloneExtent is how much of the source is worth copying: through the end of
// its last partition, not to the end of the disk.
//
// A 1 TB drive with 80 GB in use is a 1 TB copy if you take it literally, and
// the 920 GB of tail is not merely slow, it is what makes cloning onto a
// same-size drive fail when the two differ by a few sectors. Falling back to
// the whole disk when there is no partition table is deliberate: something
// unrecognised is exactly when copying everything is the safe reading.
func cloneExtent(in Target, src device.Device) (int64, error) {
	size := src.SizeBytes
	if s, err := in.Size(); err == nil && s > 0 {
		size = s
	}
	if size <= 0 {
		return 0, fmt.Errorf("clone: source %s reports no size", src.ID)
	}
	end, err := lastPartitionEnd(in)
	if err != nil || end <= 0 || end > size {
		return size, nil
	}
	// Round up to a whole chunk so the tail write stays aligned.
	if r := end % SectorSize; r != 0 {
		end += SectorSize - r
	}
	if end > size {
		return size, nil
	}
	return end, nil
}

// copyFanOut reads the source once and writes each chunk to every destination
// still healthy. A destination that fails is recorded and dropped; the others
// carry on, because one bad stick in a batch of twenty should cost one stick.
// It also hashes the source as it reads, so every copy has something to be
// checked against. Comparing the copies only to each other would leave a single
// destination — the ordinary case — verified against nothing at all.
func copyFanOut(ctx context.Context, in Target, outs []Target, dsts []device.Device,
	extent int64, results []CloneResult, progress CloneProgress) (string, error) {

	srcHash := sha256.New()
	buf := make([]byte, chunkSize)
	var done int64
	for done < extent {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := int64(chunkSize)
		if done+n > extent {
			n = extent - done
		}
		read := (n + SectorSize - 1) / SectorSize * SectorSize
		if done+read > extent {
			read = n
		}
		if _, err := in.ReadAt(buf[:read], done); err != nil {
			return "", fmt.Errorf("clone: reading source at %s: %w", gb(done), err)
		}
		srcHash.Write(buf[:n])

		var wg sync.WaitGroup
		for i := range outs {
			if results[i].Err != nil {
				continue
			}
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if _, err := outs[i].WriteAt(buf[:read], done); err != nil {
					results[i].Err = fmt.Errorf("writing at %s: %w", gb(done), err)
					return
				}
				results[i].Bytes = done + n
				progress(dsts[i].ID, "copy", done+n, extent)
			}(i)
		}
		wg.Wait()
		if firstErr(results) != nil && allFailed(results) {
			return "", firstErr(results)
		}
		done += n
	}
	return hex.EncodeToString(srcHash.Sum(nil)), nil
}

// verifyAll reads each destination back and compares it to the source bytes it
// should now hold. Without this the only evidence a clone worked is that
// nothing returned an error, and a silently truncated copy returns no error.
func verifyAll(ctx context.Context, outs []Target, dsts []device.Device,
	extent int64, srcSum string, results []CloneResult, progress CloneProgress) {

	var wg sync.WaitGroup
	for i := range outs {
		if results[i].Err != nil {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sum, err := hashDevice(ctx, outs[i], extent, func(done int64) {
				progress(dsts[i].ID, "verify", done, extent)
			})
			if err != nil {
				results[i].Err = fmt.Errorf("verify: %w", err)
				return
			}
			results[i].SHA256 = sum
		}(i)
	}
	wg.Wait()

	// Each copy is checked against the source, not against its siblings: one
	// destination is the ordinary case, and siblings would leave it compared
	// to nothing. A mismatch is a drive that accepted every write and stored
	// something else — the counterfeit-capacity stick, which reports 512 GB,
	// takes 512 GB, and holds eight.
	for i := range results {
		if results[i].Err != nil || results[i].SHA256 == "" {
			continue
		}
		if results[i].SHA256 != srcSum {
			results[i].Err = fmt.Errorf(
				"verify: %s read back different bytes than were written — the drive is not storing what it accepted",
				dsts[i].ID)
		}
	}
}

func hashDevice(ctx context.Context, t Target, extent int64, tick func(int64)) (string, error) {
	h := sha256.New()
	buf := make([]byte, chunkSize)
	var done int64
	for done < extent {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := int64(chunkSize)
		if done+n > extent {
			n = extent - done
		}
		read := (n + SectorSize - 1) / SectorSize * SectorSize
		if done+read > extent {
			read = n
		}
		if _, err := t.ReadAt(buf[:read], done); err != nil {
			return "", fmt.Errorf("reading back at %s: %w", gb(done), err)
		}
		h.Write(buf[:n])
		done += n
		tick(done)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func firstErr(rs []CloneResult) error {
	for _, r := range rs {
		if r.Err != nil {
			return fmt.Errorf("%s: %w", r.Device.ID, r.Err)
		}
	}
	return nil
}

func allFailed(rs []CloneResult) bool {
	for _, r := range rs {
		if r.Err == nil {
			return false
		}
	}
	return true
}

func gb(b int64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MiB", b>>20)
}

// equalBytes is used by tests to compare two in-memory targets.
func equalBytes(a, b []byte) bool { return bytes.Equal(a, b) }

// openTarget is the seam the tests replace. Cloning is the one operation here
// whose failure modes are silent and whose real-hardware test costs somebody a
// disk, so it is worth being able to run the whole fan-out against memory.
var openTarget = func(ctx context.Context, dev device.Device) (Target, error) {
	return OpenTarget(ctx, dev)
}
