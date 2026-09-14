package compose

import "os"

// writePS writes a generated PowerShell script with a UTF-8 byte-order mark.
// Windows PowerShell 5.1 — what firstboot.cmd runs — reads a BOM-less file
// in the ANSI code page. A UTF-8 em dash read that way ends in a curly
// closing quote, which terminates whatever string it sits in: apps.ps1
// parsed everywhere DSKY was developed and failed on every imaged machine.
// The generators now emit ASCII only, and the BOM makes the encoding
// explicit so the next non-ASCII character cannot repeat this.
func writePS(path, content string) error {
	return os.WriteFile(path, append([]byte("\xef\xbb\xbf"), content...), 0o644)
}
