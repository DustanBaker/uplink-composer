// Package vhd wraps and unwraps raw disk images as fixed VHDs.
//
// A fixed VHD is exactly the raw image followed by a 512-byte footer, so
// wrapping lets the native OS attach a Composer-built raw image (Windows
// Mount-DiskImage, macOS/Linux via qemu-nbd or loop after stripping) without
// copying the payload. Used by the verification path and the loopback
// VolumeBuilder contingency.
package vhd

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	// FooterSize is the size of a VHD footer in bytes.
	FooterSize = 512

	cookie     = "conectix"
	diskFixed  = 2
	creatorApp = "cmpr" // The Composer
	creatorOS  = "Wi2k"
)

// vhdEpoch is the VHD timestamp origin: 2000-01-01 00:00:00 UTC.
var vhdEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Footer builds a fixed-disk VHD footer for a raw image of the given size.
// size must be a multiple of 512.
func Footer(size int64, uid [16]byte, ts time.Time) ([]byte, error) {
	if size <= 0 || size%512 != 0 {
		return nil, fmt.Errorf("vhd: image size %d is not a positive multiple of 512", size)
	}
	b := make([]byte, FooterSize)
	copy(b[0:8], cookie)
	binary.BigEndian.PutUint32(b[8:12], 0x00000002)  // features: reserved bit, always set
	binary.BigEndian.PutUint32(b[12:16], 0x00010000) // format version 1.0
	binary.BigEndian.PutUint64(b[16:24], 0xFFFFFFFFFFFFFFFF)
	secs := ts.UTC().Sub(vhdEpoch).Seconds()
	if secs < 0 {
		secs = 0
	}
	binary.BigEndian.PutUint32(b[24:28], uint32(secs))
	copy(b[28:32], creatorApp)
	binary.BigEndian.PutUint32(b[32:36], 0x00010000)
	copy(b[36:40], creatorOS)
	binary.BigEndian.PutUint64(b[40:48], uint64(size)) // original size
	binary.BigEndian.PutUint64(b[48:56], uint64(size)) // current size
	c, h, s := chsGeometry(size / 512)
	binary.BigEndian.PutUint16(b[56:58], c)
	b[58] = h
	b[59] = s
	binary.BigEndian.PutUint32(b[60:64], diskFixed)
	copy(b[68:84], uid[:])
	// b[84] saved state = 0; rest zeros.
	binary.BigEndian.PutUint32(b[64:68], checksum(b))
	return b, nil
}

// chsGeometry implements the geometry algorithm from the VHD specification.
func chsGeometry(totalSectors int64) (cylinders uint16, heads, sectorsPerTrack byte) {
	if totalSectors > 65535*16*255 {
		totalSectors = 65535 * 16 * 255
	}
	var spt, hds, cylTimesHeads int64
	if totalSectors >= 65535*16*63 {
		spt = 255
		hds = 16
		cylTimesHeads = totalSectors / spt
	} else {
		spt = 17
		cylTimesHeads = totalSectors / spt
		hds = (cylTimesHeads + 1023) / 1024
		if hds < 4 {
			hds = 4
		}
		if cylTimesHeads >= hds*1024 || hds > 16 {
			spt = 31
			hds = 16
			cylTimesHeads = totalSectors / spt
		}
		if cylTimesHeads >= hds*1024 {
			spt = 63
			hds = 16
			cylTimesHeads = totalSectors / spt
		}
	}
	return uint16(cylTimesHeads / hds), byte(hds), byte(spt)
}

// checksum is the ones' complement of the byte sum with the checksum field zeroed.
func checksum(footer []byte) uint32 {
	var sum uint32
	for i, v := range footer {
		if i >= 64 && i < 68 {
			continue
		}
		sum += uint32(v)
	}
	return ^sum
}

// Wrap copies raw image src to dst and appends a fixed-VHD footer.
func Wrap(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	var uid [16]byte
	if _, err := rand.Read(uid[:]); err != nil {
		return err
	}
	footer, err := Footer(st.Size(), uid, time.Now())
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if _, err := out.Write(footer); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// AppendFooter appends a fixed-VHD footer to path in place, turning a raw
// image into a fixed VHD without copying. StripFooter reverses it.
func AppendFooter(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	var uid [16]byte
	if _, err := rand.Read(uid[:]); err != nil {
		return err
	}
	footer, err := Footer(st.Size(), uid, time.Now())
	if err != nil {
		return err
	}
	if _, err := f.WriteAt(footer, st.Size()); err != nil {
		return err
	}
	return f.Sync()
}

// StripFooter removes a trailing fixed-VHD footer from path in place after
// validating the cookie, restoring the raw image.
func StripFooter(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() < FooterSize {
		return fmt.Errorf("vhd: %s is smaller than a footer", path)
	}
	got := make([]byte, 8)
	if _, err := f.ReadAt(got, st.Size()-FooterSize); err != nil {
		return err
	}
	if !bytes.Equal(got, []byte(cookie)) {
		return fmt.Errorf("vhd: %s has no VHD footer to strip", path)
	}
	return f.Truncate(st.Size() - FooterSize)
}
