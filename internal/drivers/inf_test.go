package drivers

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

const sampleINF = `; Sample network INF
[Version]
Signature   = "$WINDOWS NT$"
Class       = Net
ClassGUID   = {4d36e972-e325-11ce-bfc1-08002be10318}
Provider    = %Vendor%
DriverVer   = 05/07/2025,12.19.2.64

[Manufacturer]
%Vendor% = Models, NTamd64.10.0

[Models.NTamd64.10.0]
%Dev.I219LM% = InstallSec, PCI\VEN_8086&DEV_15B7
%Dev.I219V%  = InstallSec, PCI\VEN_8086&DEV_15B8, PCI\VEN_8086&DEV_15B8&SUBSYS_00008086

[InstallSec]
AddReg = Nothing

[Strings]
Vendor     = "Contoso Networks"
Dev.I219LM = "Contoso Ethernet I219-LM"
Dev.I219V  = "Contoso Ethernet I219-V"
`

func TestParseINF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "e1d.inf")
	if err := os.WriteFile(p, []byte(sampleINF), 0o644); err != nil {
		t.Fatal(err)
	}
	inf, err := ParseINF(p)
	if err != nil {
		t.Fatal(err)
	}
	if inf.Class != "Net" {
		t.Errorf("class = %q", inf.Class)
	}
	if inf.Provider != "Contoso Networks" {
		t.Errorf("provider = %q (strings resolution)", inf.Provider)
	}
	if inf.DriverVer != "12.19.2.64" || inf.DriverDate != "05/07/2025" {
		t.Errorf("ver = %q date = %q", inf.DriverVer, inf.DriverDate)
	}
	if len(inf.HardwareIDs) != 3 {
		t.Errorf("hardware IDs = %v, want 3", inf.HardwareIDs)
	}
	found := false
	for _, id := range inf.HardwareIDs {
		if id == `PCI\VEN_8086&DEV_15B8&SUBSYS_00008086` {
			found = true
		}
	}
	if !found {
		t.Errorf("missing SUBSYS id in %v", inf.HardwareIDs)
	}
	if len(inf.Devices) != 2 || inf.Devices[0] != "Contoso Ethernet I219-LM" {
		t.Errorf("devices = %v", inf.Devices)
	}
}

// TestParseINFUTF16 covers the UTF-16LE encoding many vendor INFs ship.
func TestParseINFUTF16(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "u16.inf")
	u := utf16.Encode([]rune(sampleINF))
	raw := make([]byte, 2+len(u)*2)
	raw[0], raw[1] = 0xFF, 0xFE
	for i, v := range u {
		binary.LittleEndian.PutUint16(raw[2+i*2:], v)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	inf, err := ParseINF(p)
	if err != nil {
		t.Fatal(err)
	}
	if inf.Provider != "Contoso Networks" || len(inf.HardwareIDs) != 3 {
		t.Errorf("utf16 parse: provider %q ids %v", inf.Provider, inf.HardwareIDs)
	}
}

func TestScanDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.inf", "sub/b.INF"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte(sampleINF), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	infs, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infs) != 2 {
		t.Fatalf("found %d INFs, want 2", len(infs))
	}
	if infs[1].File != "sub/b.INF" {
		t.Errorf("relative path = %q", infs[1].File)
	}
}
