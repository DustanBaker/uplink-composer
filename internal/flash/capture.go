package flash

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/uplinkresearch/dsky/internal/device"
)

// Capture reads a device into outPath, from sector 0 through the end of its
// last partition (the "capture the golden master stick" workflow).
// Returns "sha256:<hex>:<bytes>" of the written image.
//
// GPT note: the on-disk backup GPT at the very end of the device is not
// captured; writing the image to another stick leaves it absent until a
// repair (Windows and Linux mount such disks fine; gdisk/diskpart can
// rebuild it). DSKY-built media is MBR and unaffected.
func Capture(ctx context.Context, dev device.Device, outPath string, progress Progress) (string, error) {
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	if dev.System {
		return "", fmt.Errorf("capture: refusing %s: it hosts the running OS", dev.ID)
	}
	t, err := OpenTarget(ctx, dev)
	if err != nil {
		return "", err
	}
	defer t.Close()

	devSize := dev.SizeBytes
	if s, err := t.Size(); err == nil && s > 0 {
		devSize = s
	}

	end, err := lastPartitionEnd(t)
	if err != nil {
		return "", err
	}
	if end <= 0 || end > devSize {
		return "", fmt.Errorf("capture: %s has no usable partition table (last partition ends at %d of %d)", dev.ID, end, devSize)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	buf := make([]byte, chunkSize)
	var read int64
	for read < end {
		if err := ctx.Err(); err != nil {
			out.Close()
			os.Remove(outPath)
			return "", err
		}
		n := int64(chunkSize)
		if read+n > end {
			// Keep device reads sector-aligned; trim before writing out.
			n = end - read
			aligned := (n + SectorSize - 1) / SectorSize * SectorSize
			if _, err := t.ReadAt(buf[:aligned], read); err != nil {
				out.Close()
				os.Remove(outPath)
				return "", fmt.Errorf("capture: read failed at %d MiB: %w", read>>20, err)
			}
		} else if _, err := t.ReadAt(buf[:n], read); err != nil {
			out.Close()
			os.Remove(outPath)
			return "", fmt.Errorf("capture: read failed at %d MiB: %w", read>>20, err)
		}
		if _, err := out.Write(buf[:n]); err != nil {
			out.Close()
			os.Remove(outPath)
			return "", err
		}
		hash.Write(buf[:n])
		read += n
		progress("capture", read, end)
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%s:%d", hex.EncodeToString(hash.Sum(nil)), read), nil
}

// lastPartitionEnd parses the MBR (or GPT behind a protective MBR) directly
// from the device and returns the byte offset just past the last partition.
func lastPartitionEnd(t Target) (int64, error) {
	sector := make([]byte, SectorSize)
	if _, err := t.ReadAt(sector, 0); err != nil {
		return 0, fmt.Errorf("capture: reading MBR: %w", err)
	}
	if sector[510] != 0x55 || sector[511] != 0xAA {
		return 0, fmt.Errorf("capture: no partition table signature on device")
	}
	var maxEnd int64
	isGPT := false
	for i := 0; i < 4; i++ {
		e := sector[446+i*16 : 446+(i+1)*16]
		ptype := e[4]
		if ptype == 0 {
			continue
		}
		if ptype == 0xEE {
			isGPT = true
		}
		start := int64(binary.LittleEndian.Uint32(e[8:12]))
		size := int64(binary.LittleEndian.Uint32(e[12:16]))
		if end := (start + size) * SectorSize; end > maxEnd {
			maxEnd = end
		}
	}
	if !isGPT {
		return maxEnd, nil
	}

	// GPT: header at LBA 1 points at the entry array.
	hdr := make([]byte, SectorSize)
	if _, err := t.ReadAt(hdr, SectorSize); err != nil {
		return 0, fmt.Errorf("capture: reading GPT header: %w", err)
	}
	if string(hdr[0:8]) != "EFI PART" {
		return 0, fmt.Errorf("capture: protective MBR without a GPT header")
	}
	entriesLBA := int64(binary.LittleEndian.Uint64(hdr[72:80]))
	count := int64(binary.LittleEndian.Uint32(hdr[80:84]))
	entrySize := int64(binary.LittleEndian.Uint32(hdr[84:88]))
	if entrySize < 128 || entrySize > 4096 || count < 1 || count > 1024 {
		return 0, fmt.Errorf("capture: implausible GPT entry geometry (%d x %d)", count, entrySize)
	}
	tableBytes := count * entrySize
	alloc := (tableBytes + SectorSize - 1) / SectorSize * SectorSize
	table := make([]byte, alloc)
	if _, err := t.ReadAt(table, entriesLBA*SectorSize); err != nil {
		return 0, fmt.Errorf("capture: reading GPT entries: %w", err)
	}
	maxEnd = entriesLBA*SectorSize + alloc // at minimum keep the entry array
	zeroGUID := make([]byte, 16)
	for i := int64(0); i < count; i++ {
		e := table[i*entrySize : (i+1)*entrySize]
		if string(e[0:16]) == string(zeroGUID) {
			continue
		}
		endLBA := int64(binary.LittleEndian.Uint64(e[40:48]))
		if end := (endLBA + 1) * SectorSize; end > maxEnd {
			maxEnd = end
		}
	}
	return maxEnd, nil
}
