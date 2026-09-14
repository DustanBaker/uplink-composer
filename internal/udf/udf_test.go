package udf

import (
	"bytes"
	"errors"
	"testing"
)

func TestDecodeName(t *testing.T) {
	if got := decodeName(append([]byte{8}, "SETUP.EXE"...)); got != "SETUP.EXE" {
		t.Errorf("8-bit: %q", got)
	}
	// "café" in UTF-16BE after compression id 16.
	if got := decodeName([]byte{16, 0, 'c', 0, 'a', 0, 'f', 0, 0xE9}); got != "café" {
		t.Errorf("16-bit: %q", got)
	}
	if decodeName(nil) != "" || decodeName([]byte{254, 'x'}) != "" {
		t.Error("unknown compression decoded")
	}
}

// Real images are checked against Linux's UDF driver in the media-formats
// workflow, and a Windows 11 ISO was compared file for file on 2026-09-14.
func TestOpenRejectsNonUDF(t *testing.T) {
	if _, err := Open(bytes.NewReader(make([]byte, 1<<20))); !errors.Is(err, ErrNotUDF) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Open(bytes.NewReader(make([]byte, 100))); !errors.Is(err, ErrNotUDF) {
		t.Fatalf("short image: err = %v", err)
	}
}

func TestSafeJoin(t *testing.T) {
	got, err := safeJoin("/out", "../../etc/passwd")
	if err != nil || got != "/out/etc/passwd" {
		t.Fatalf("%q %v", got, err)
	}
}
