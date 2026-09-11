// Package helpers runs the few external tools composing needs, preferring
// OS-native tooling so most hosts need zero downloads:
//
//	ISO extract:  Windows Mount-DiskImage · macOS hdiutil · Linux 7zz
//	WIM split:    Windows DISM · macOS/Linux wimlib-imagex
//
// Everything is invoked as a subprocess (never linked), which also keeps the
// GPL boundary around wimlib clean.
package helpers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Status describes one tool requirement for `composer doctor`.
type Status struct {
	Name     string
	Purpose  string
	Path     string // resolved location, empty if missing
	Builtin  bool   // ships with the OS
	Required bool   // needed for the Windows-media pipeline on this host
	Hint     string // how to install when missing
}

// run executes a tool, returning combined output in the error on failure.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Find7z locates a 7-Zip console binary: an explicit COMPOSER_7Z override,
// then PATH (7zz preferred, then 7z), then the library helpers dir.
func Find7z(helpersDir string) (string, error) {
	if env := os.Getenv("COMPOSER_7Z"); env != "" {
		return env, nil
	}
	for _, name := range []string{"7zz", "7z"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	for _, name := range []string{"7zz", "7z", "7zz.exe", "7z.exe"} {
		p := filepath.Join(helpersDir, "7zip", name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("7-Zip console binary not found: install it (apt/dnf install 7zip, or download 7zz from 7-zip.org into %s)", filepath.Join(helpersDir, "7zip"))
}

// FindWimlib locates wimlib-imagex for non-Windows hosts.
func FindWimlib(helpersDir string) (string, error) {
	if env := os.Getenv("COMPOSER_WIMLIB"); env != "" {
		return env, nil
	}
	for _, name := range []string{"wimlib-imagex", "wimsplit"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	p := filepath.Join(helpersDir, "wimlib", "wimlib-imagex")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("wimlib-imagex not found: install wimlib (brew install wimlib / apt install wimtools)")
}

// ExtractISO extracts the full contents of an ISO (Windows media is UDF)
// into destDir using the platform-native reader.
func ExtractISO(ctx context.Context, helpersDir, isoPath, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	abs, err := filepath.Abs(isoPath)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		// Mount-DiskImage reads UDF natively; robocopy exit codes 0-7 mean
		// success. The finally block guarantees dismount.
		script := fmt.Sprintf(`$ErrorActionPreference='Stop'
$iso = %q
$dest = %q
Mount-DiskImage -ImagePath $iso -Access ReadOnly | Out-Null
try {
  $vol = Get-DiskImage -ImagePath $iso | Get-Volume
  $root = $vol.DriveLetter + ':\'
  robocopy $root $dest /E /R:2 /W:2 /MT:8 /NFL /NDL /NJH /NJS | Out-Null
  if ($LASTEXITCODE -ge 8) { throw "robocopy failed with exit code $LASTEXITCODE" }
} finally {
  Dismount-DiskImage -ImagePath $iso | Out-Null
}`, abs, destDir)
		return run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	case "darwin":
		mnt, err := os.MkdirTemp("", "composer-iso")
		if err != nil {
			return err
		}
		defer os.Remove(mnt)
		if err := run(ctx, "hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mnt, abs); err != nil {
			return err
		}
		cpErr := run(ctx, "cp", "-R", mnt+"/.", destDir)
		detachErr := run(ctx, "hdiutil", "detach", mnt)
		if cpErr != nil {
			return cpErr
		}
		return detachErr
	default:
		sevenZip, err := Find7z(helpersDir)
		if err != nil {
			return err
		}
		return run(ctx, sevenZip, "x", "-y", "-o"+destDir, abs)
	}
}

// SplitWIM splits wimPath into ≤chunkMB .swm parts written next to swmPath
// (which must end in .swm). Required whenever install.wim exceeds the FAT32
// 4 GiB file limit — true for every Win11 24H2+ ISO.
func SplitWIM(ctx context.Context, helpersDir, wimPath, swmPath string, chunkMB int) error {
	if chunkMB <= 0 {
		chunkMB = 3800
	}
	if runtime.GOOS == "windows" {
		return run(ctx, "dism",
			"/Split-Image",
			"/ImageFile:"+wimPath,
			"/SWMFile:"+swmPath,
			fmt.Sprintf("/FileSize:%d", chunkMB))
	}
	wimlib, err := FindWimlib(helpersDir)
	if err != nil {
		return err
	}
	if filepath.Base(wimlib) == "wimsplit" {
		return run(ctx, wimlib, wimPath, swmPath, fmt.Sprintf("%d", chunkMB))
	}
	return run(ctx, wimlib, "split", wimPath, swmPath, fmt.Sprintf("%d", chunkMB))
}

// ExpandZip extracts a .zip driver pack into destDir (pure Go, used by the
// compose pipeline for inf-dir packs).
// Implemented in zip.go.

// Check reports the tool situation on this host for `composer doctor`.
func Check(helpersDir string) []Status {
	var out []Status
	switch runtime.GOOS {
	case "windows":
		out = append(out,
			Status{Name: "Mount-DiskImage", Purpose: "ISO (UDF) extraction", Path: "powershell (built in)", Builtin: true, Required: true},
			Status{Name: "DISM", Purpose: "WIM splitting for FAT32", Path: lookPathOr("dism", ""), Builtin: true, Required: true, Hint: "part of Windows"},
		)
	case "darwin":
		st := Status{Name: "hdiutil", Purpose: "ISO (UDF) extraction", Builtin: true, Required: true}
		st.Path = lookPathOr("hdiutil", "")
		out = append(out, st)
		w := Status{Name: "wimlib-imagex", Purpose: "WIM splitting for FAT32", Required: true, Hint: "brew install wimlib"}
		w.Path, _ = pathOrEmpty(FindWimlib(helpersDir))
		out = append(out, w)
	default:
		s := Status{Name: "7zz", Purpose: "ISO (UDF) extraction", Required: true, Hint: "apt/dnf install 7zip, or place 7zz under " + filepath.Join(helpersDir, "7zip")}
		s.Path, _ = pathOrEmpty(Find7z(helpersDir))
		out = append(out, s)
		w := Status{Name: "wimlib-imagex", Purpose: "WIM splitting for FAT32", Required: true, Hint: "apt install wimtools"}
		w.Path, _ = pathOrEmpty(FindWimlib(helpersDir))
		out = append(out, w)
	}
	return out
}

func lookPathOr(name, fallback string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return fallback
}

func pathOrEmpty(p string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return p, nil
}
