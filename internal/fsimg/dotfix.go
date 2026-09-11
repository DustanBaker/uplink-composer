package fsimg

import (
	"encoding/binary"
	"fmt"
	"os"
)

// fixRootDotDot repairs a FAT32 spec deviation in go-diskfs: the ".." entry
// of a directory whose parent is the root must store cluster 0, but go-diskfs
// writes the root's real cluster number. chkdsk flags every top-level
// directory with "Errors in . and/or .. corrected" otherwise (found by the S1
// native verification). Operates directly on the finished image with
// self-contained FAT math; the FAT tables themselves need no change.
func fixRootDotDot(imgPath string, partStartBytes int64) error {
	f, err := os.OpenFile(imgPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	// Boot sector geometry.
	bs := make([]byte, 512)
	if _, err := f.ReadAt(bs, partStartBytes); err != nil {
		return fmt.Errorf("fsimg: reading boot sector: %w", err)
	}
	bytesPerSector := int64(binary.LittleEndian.Uint16(bs[11:13]))
	sectorsPerCluster := int64(bs[13])
	reservedSectors := int64(binary.LittleEndian.Uint16(bs[14:16]))
	numFATs := int64(bs[16])
	sectorsPerFAT := int64(binary.LittleEndian.Uint32(bs[36:40]))
	rootCluster := binary.LittleEndian.Uint32(bs[44:48])
	if bytesPerSector == 0 || sectorsPerCluster == 0 || sectorsPerFAT == 0 {
		return fmt.Errorf("fsimg: implausible FAT32 boot sector at offset %d", partStartBytes)
	}
	bytesPerCluster := bytesPerSector * sectorsPerCluster
	fatStart := partStartBytes + reservedSectors*bytesPerSector
	dataStart := fatStart + numFATs*sectorsPerFAT*bytesPerSector
	clusterOffset := func(n uint32) int64 { return dataStart + int64(n-2)*bytesPerCluster }

	nextCluster := func(n uint32) (uint32, error) {
		var b [4]byte
		if _, err := f.ReadAt(b[:], fatStart+int64(n)*4); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(b[:]) & 0x0FFFFFFF, nil
	}

	// Walk the root directory's cluster chain collecting top-level
	// subdirectory first-clusters.
	var subdirs []uint32
	cluster := rootCluster
	buf := make([]byte, bytesPerCluster)
	for cluster >= 2 && cluster < 0x0FFFFFF8 {
		if _, err := f.ReadAt(buf, clusterOffset(cluster)); err != nil {
			return fmt.Errorf("fsimg: reading root directory cluster %d: %w", cluster, err)
		}
		for off := int64(0); off+32 <= bytesPerCluster; off += 32 {
			e := buf[off : off+32]
			switch {
			case e[0] == 0x00: // end of directory
				cluster = 0x0FFFFFF8
				off = bytesPerCluster
			case e[0] == 0xE5: // deleted
			case e[11]&0x0F == 0x0F: // long-name entry
			case e[11]&0x08 != 0: // volume label
			case e[11]&0x10 != 0 && e[0] != '.': // subdirectory
				c := uint32(binary.LittleEndian.Uint16(e[20:22]))<<16 |
					uint32(binary.LittleEndian.Uint16(e[26:28]))
				if c >= 2 {
					subdirs = append(subdirs, c)
				}
			}
		}
		if cluster >= 0x0FFFFFF8 {
			break
		}
		next, err := nextCluster(cluster)
		if err != nil {
			return err
		}
		cluster = next
	}

	// In each top-level subdirectory, entry 1 must be ".." — zero its
	// cluster fields.
	for _, c := range subdirs {
		entry := make([]byte, 32)
		off := clusterOffset(c) + 32 // entry index 1
		if _, err := f.ReadAt(entry, off); err != nil {
			return fmt.Errorf("fsimg: reading dot-dot entry of cluster %d: %w", c, err)
		}
		if entry[0] != '.' || entry[1] != '.' {
			return fmt.Errorf("fsimg: directory at cluster %d has no dot-dot entry where expected", c)
		}
		already := binary.LittleEndian.Uint16(entry[20:22]) == 0 &&
			binary.LittleEndian.Uint16(entry[26:28]) == 0
		if already {
			continue
		}
		binary.LittleEndian.PutUint16(entry[20:22], 0)
		binary.LittleEndian.PutUint16(entry[26:28], 0)
		if _, err := f.WriteAt(entry, off); err != nil {
			return fmt.Errorf("fsimg: fixing dot-dot entry of cluster %d: %w", c, err)
		}
	}
	return f.Sync()
}

// rootDotDotClusters reports the ".." cluster value of every top-level
// directory — test support for asserting the fix.
func rootDotDotClusters(imgPath string, partStartBytes int64) (map[uint32]uint32, error) {
	f, err := os.Open(imgPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	bs := make([]byte, 512)
	if _, err := f.ReadAt(bs, partStartBytes); err != nil {
		return nil, err
	}
	bytesPerSector := int64(binary.LittleEndian.Uint16(bs[11:13]))
	sectorsPerCluster := int64(bs[13])
	reservedSectors := int64(binary.LittleEndian.Uint16(bs[14:16]))
	numFATs := int64(bs[16])
	sectorsPerFAT := int64(binary.LittleEndian.Uint32(bs[36:40]))
	rootCluster := binary.LittleEndian.Uint32(bs[44:48])
	bytesPerCluster := bytesPerSector * sectorsPerCluster
	fatStart := partStartBytes + reservedSectors*bytesPerSector
	dataStart := fatStart + numFATs*sectorsPerFAT*bytesPerSector
	clusterOffset := func(n uint32) int64 { return dataStart + int64(n-2)*bytesPerCluster }

	out := map[uint32]uint32{}
	buf := make([]byte, bytesPerCluster)
	cluster := rootCluster
	for cluster >= 2 && cluster < 0x0FFFFFF8 {
		if _, err := f.ReadAt(buf, clusterOffset(cluster)); err != nil {
			return nil, err
		}
		done := false
		for off := int64(0); off+32 <= bytesPerCluster; off += 32 {
			e := buf[off : off+32]
			if e[0] == 0x00 {
				done = true
				break
			}
			if e[0] == 0xE5 || e[11]&0x0F == 0x0F || e[11]&0x08 != 0 || e[11]&0x10 == 0 || e[0] == '.' {
				continue
			}
			c := uint32(binary.LittleEndian.Uint16(e[20:22]))<<16 |
				uint32(binary.LittleEndian.Uint16(e[26:28]))
			if c < 2 {
				continue
			}
			entry := make([]byte, 32)
			if _, err := f.ReadAt(entry, clusterOffset(c)+32); err != nil {
				return nil, err
			}
			out[c] = uint32(binary.LittleEndian.Uint16(entry[20:22]))<<16 |
				uint32(binary.LittleEndian.Uint16(entry[26:28]))
		}
		if done {
			break
		}
		var b [4]byte
		if _, err := f.ReadAt(b[:], fatStart+int64(cluster)*4); err != nil {
			return nil, err
		}
		cluster = binary.LittleEndian.Uint32(b[:]) & 0x0FFFFFFF
	}
	return out, nil
}
