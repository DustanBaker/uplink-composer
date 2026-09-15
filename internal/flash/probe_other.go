//go:build !windows

package flash

import (
	"errors"
	"io"
)

// Probe is a Windows diagnostic; nothing else needs it.
func Probe(device string, wantSize int64, out io.Writer) error {
	return errors.New("disk-probe only does anything on Windows")
}
