package diskutil

import "testing"

func TestOnDisk(t *testing.T) {
	for _, c := range []struct {
		node, disk string
		want       bool
	}{
		{"/dev/sda", "/dev/sda", true},
		{"/dev/sda1", "/dev/sda", true},
		{"/dev/sda12", "/dev/sda", true},
		{"/dev/sdaa", "/dev/sda", false},
		{"/dev/sdaa1", "/dev/sda", false},
		{"/dev/nvme0n1p2", "/dev/nvme0n1", true},
		{"/dev/nvme0n10", "/dev/nvme0n1", false}, // namespace 10, another disk
		{"/dev/mmcblk0p1", "/dev/mmcblk0", true},
		{"/dev/mapper/root", "/dev/sda", false},
	} {
		if got := onDisk(c.node, c.disk); got != c.want {
			t.Errorf("onDisk(%s, %s) = %v", c.node, c.disk, got)
		}
	}
}
