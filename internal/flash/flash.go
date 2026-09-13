// Package flash writes artifacts to raw USB devices: aligned chunked
// writes, streaming decompression, a stale-GPT tail wipe, and readback
// verification. Safety gates: only device.Flashable() targets, size checks,
// and the caller-supplied typed confirmation upstream.
package flash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/uplinkresearch/bootwright/internal/compose"
	"github.com/uplinkresearch/bootwright/internal/device"
	"github.com/uplinkresearch/bootwright/internal/stream"
)

// SectorSize is the only supported logical sector size; Target opens verify
// this and refuse 4Kn devices with a clear error.
const SectorSize = 512

// chunkSize is the aligned write unit.
const chunkSize = 4 << 20

// tailWipe is how much is zeroed at the end of the device (stale GPT backup
// headers from the stick's previous life confuse firmware).
const tailWipe = 2 << 20

// Progress receives stage updates; total is -1 while unknown (compressed
// sources).
type Progress func(stage string, done, total int64)

// Target is an open raw device.
type Target interface {
	Size() (int64, error)
	WriteAt(p []byte, off int64) (int, error)
	ReadAt(p []byte, off int64) (int, error)
	Sync() error
	// Finalize refreshes the OS's view of the device after writing.
	Finalize() error
	Close() error
}

// Flash writes the artifact to dev and verifies by readback unless the
// artifact says verify none. progress may be nil.
func Flash(ctx context.Context, art *compose.Artifact, dev device.Device, progress Progress) error {
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	if !dev.Flashable() {
		return fmt.Errorf("flash: refusing %s: not a removable USB device (bus=%s system=%v)", dev.ID, dev.Bus, dev.System)
	}
	if dev.System {
		return fmt.Errorf("flash: refusing %s: it hosts the running OS", dev.ID)
	}
	if art.MinStick > 0 && dev.SizeBytes > 0 && dev.SizeBytes < art.MinStick {
		return fmt.Errorf("flash: %s is %d MiB, recipe requires at least %d MiB", dev.ID, dev.SizeBytes>>20, art.MinStick>>20)
	}

	reader, err := stream.Open(art.Path, art.Compress)
	if err != nil {
		return fmt.Errorf("flash: %w", err)
	}
	defer reader.Close()
	total := art.Size
	if art.Compress != "" && art.Compress != "none" {
		total = -1
	}

	progress("open", 0, total)
	t, err := OpenTarget(ctx, dev)
	if err != nil {
		return err
	}
	defer t.Close()
	// The open device's own size report is authoritative where available
	// (Windows WMI enumeration under-reports by CHS-geometry rounding, up
	// to a few MiB). The enumerator's size still cross-checks that the
	// same physical device is attached: a swapped stick differs by far
	// more than one cylinder.
	const enumTolerance = 64 << 20
	devSize := dev.SizeBytes
	if tSize, err := t.Size(); err == nil && tSize > 0 {
		switch {
		case devSize == 0:
			devSize = tSize
		case tSize < devSize || tSize-devSize > enumTolerance:
			return fmt.Errorf("flash: %s size mismatch: enumeration saw %d, device reports %d — device changed since listing; replug and re-run `bootwright devices`", dev.ID, devSize, tSize)
		default:
			devSize = tSize
		}
	} else if devSize == 0 {
		return fmt.Errorf("flash: cannot determine size of %s", dev.ID)
	}
	if devSize%SectorSize != 0 {
		return fmt.Errorf("flash: %s reports size %d, not a multiple of %d", dev.ID, devSize, SectorSize)
	}
	if total > 0 && total > devSize {
		return fmt.Errorf("flash: artifact is %d MiB but %s holds only %d MiB", total>>20, dev.ID, devSize>>20)
	}

	// ── Tail wipe (stale GPT backup headers) ─────────────────────────────
	// Done BEFORE the image write: once the new partition table lands,
	// Windows re-enumerates the disk and blocks late raw writes near the
	// end ("Incorrect function" — found on the first physical stick). At
	// this point the old table is still in place and every old volume is
	// locked, so end-of-disk writes are permitted. The image write may
	// later overlap this region harmlessly.
	zeros := make([]byte, tailWipe)
	if _, err := t.WriteAt(zeros, devSize-tailWipe); err != nil {
		return fmt.Errorf("flash: wiping stale partition data at end of device: %w", err)
	}

	// ── Write ────────────────────────────────────────────────────────────
	hash := sha256.New()
	buf := make([]byte, chunkSize)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("flash: canceled after %d MiB: %w", written>>20, err)
		}
		n, rerr := io.ReadFull(reader, buf)
		if n > 0 {
			chunk := buf[:n]
			if pad := (SectorSize - n%SectorSize) % SectorSize; pad != 0 {
				chunk = buf[:n+pad]
				for i := n; i < n+pad; i++ {
					chunk[i] = 0
				}
			}
			if written+int64(len(chunk)) > devSize {
				return fmt.Errorf("flash: image is larger than %s (%d MiB) — wrong device or truncated stick", dev.ID, devSize>>20)
			}
			if _, werr := t.WriteAt(chunk, written); werr != nil {
				return fmt.Errorf("flash: write failed at %d MiB (device unplugged?): %w", written>>20, werr)
			}
			hash.Write(chunk)
			written += int64(len(chunk))
			progress("write", written, total)
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("flash: reading artifact: %w", rerr)
		}
	}
	if written == 0 {
		return fmt.Errorf("flash: artifact was empty")
	}
	wantHash := hex.EncodeToString(hash.Sum(nil))

	if err := t.Sync(); err != nil {
		return fmt.Errorf("flash: flushing device: %w", err)
	}

	// ── Verify ───────────────────────────────────────────────────────────
	if art.Verify != "none" {
		progress("verify", 0, written)
		vhash := sha256.New()
		var read int64
		for read < written {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := int64(chunkSize)
			if read+n > written {
				n = written - read
			}
			if _, err := t.ReadAt(buf[:n], read); err != nil {
				return fmt.Errorf("flash: readback failed at %d MiB: %w", read>>20, err)
			}
			vhash.Write(buf[:n])
			read += n
			progress("verify", read, written)
		}
		got := hex.EncodeToString(vhash.Sum(nil))
		if got != wantHash {
			return fmt.Errorf("flash: VERIFY FAILED on %s — device returned different data than written (failing or counterfeit stick). Do not use this stick", dev.ID)
		}
	}

	if err := t.Finalize(); err != nil {
		return fmt.Errorf("flash: finalizing device: %w", err)
	}
	progress("done", written, written)
	return nil
}
