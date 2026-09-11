package helpers

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExpandZip extracts a .zip archive (driver packs) into destDir with
// zip-slip protection.
func ExpandZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", zipPath, err)
	}
	defer r.Close()
	destAbs, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	for _, f := range r.File {
		name := filepath.FromSlash(f.Name)
		target := filepath.Join(destAbs, name)
		if rel, err := filepath.Rel(destAbs, target); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("zip entry %q escapes destination", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("extracting %s: %w", f.Name, err)
		}
	}
	return nil
}
