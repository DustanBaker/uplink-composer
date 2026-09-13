// Command catalogen generates, signs and verifies the published catalog
// index.
//
// The index is produced from the list compiled into the program, never
// written by hand beside it, so the two cannot drift. Signing happens here,
// on the maintainer's machine, with a key that never goes near CI — the
// point of signing the catalog is that the hosting and the build system do
// not have to be trusted with what lands on people's disks.
//
//	go run ./test/catalogen -keygen -key <path>     once, to create a key
//	go run ./test/catalogen -key <path>             regenerate and sign
//	go run ./test/catalogen -verify                 check what is committed
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uplinkresearch/dsky/internal/oscatalog"
)

func main() {
	var (
		keygen  = flag.Bool("keygen", false, "create a signing key and print its public half")
		verify  = flag.Bool("verify", false, "verify the committed index against the built-in public key")
		keyPath = flag.String("key", defaultKeyPath(), "private signing key")
		outDir  = flag.String("out", "catalog", "directory to write index.json and index.json.sig into")
	)
	flag.Parse()

	switch {
	case *keygen:
		die(doKeygen(*keyPath))
	case *verify:
		die(doVerify(*outDir))
	default:
		die(doGenerate(*keyPath, *outDir))
	}
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// defaultKeyPath keeps the key with the user's other app configuration
// rather than in the repository, where it would eventually be committed.
func defaultKeyPath() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "dsky", "catalog-signing-key")
	}
	return "catalog-signing-key"
}

func doKeygen(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists — refusing to overwrite a signing key", path)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// 0600: this key is the only thing standing between a compromised host
	// and a forged catalog.
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("Private key written to %s — back it up; losing it means every\n", path)
	fmt.Printf("installed copy stops accepting catalog updates until they are rebuilt.\n\n")
	fmt.Printf("Put this in catalogPublicKey in internal/oscatalog/index.go:\n\n  %s\n",
		hex.EncodeToString(pub))
	return nil
}

func doGenerate(keyPath, outDir string) error {
	priv, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	// Serial only moves forward, so a signed-but-old index cannot be
	// replayed at anyone. Seconds since epoch is monotonic in practice and
	// needs no state carried between runs.
	serial := time.Now().Unix()
	if old, err := readIndex(outDir); err == nil && old.Serial >= serial {
		serial = old.Serial + 1
	}
	payload, err := oscatalog.Marshal(oscatalog.BuildIndex(serial, time.Now()))
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, payload)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "index.json"), payload, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "index.json.sig"),
		[]byte(hex.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return err
	}
	idx, err := oscatalog.VerifyIndexBytes(payload, []byte(hex.EncodeToString(sig)))
	if err != nil {
		return fmt.Errorf("signed an index this build will not accept: %w", err)
	}
	fmt.Printf("catalog/index.json  serial %d, %d operating systems, signature verified\n",
		idx.Serial, len(idx.Entries))
	return nil
}

func doVerify(outDir string) error {
	payload, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(outDir, "index.json.sig"))
	if err != nil {
		return err
	}
	idx, err := oscatalog.VerifyIndexBytes(payload, sig)
	if err != nil {
		return err
	}
	fmt.Printf("index.json  serial %d, %d operating systems, signature verified\n",
		idx.Serial, len(idx.Entries))
	return nil
}

func readIndex(outDir string) (*oscatalog.Index, error) {
	b, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		return nil, err
	}
	var idx oscatalog.Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w\n(create one with: go run ./test/catalogen -keygen)", err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("%s is not a hex key: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is %d bytes, want %d", path, len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}
