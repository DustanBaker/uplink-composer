// Package agentbin carries the first-boot agent DSKY stages onto Windows
// media. DSKY runs on Linux, macOS and Windows and has to put a Windows
// program on the stick from any of them, so the agent is compiled first and
// embedded here, gzipped.
//
// The files are placeholders in the repository and are filled in by the
// build (`make agent`, and the release workflow). A DSKY built without that
// step has no agent, Available reports false, and compose falls back to the
// generated scripts — a build that skipped a step produces older media, not
// broken media.
package agentbin

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
)

//go:embed bin
var files embed.FS

// Arch is a Windows architecture the agent is built for.
type Arch string

const (
	AMD64 Arch = "amd64"
	ARM64 Arch = "arm64"
)

// Available reports whether a real agent was embedded for arch.
func Available(arch Arch) bool {
	b, err := files.ReadFile("bin/dsky-agent-" + string(arch) + ".exe.gz")
	return err == nil && len(b) > 0
}

// Binary returns the agent for arch, decompressed.
func Binary(arch Arch) ([]byte, error) {
	b, err := files.ReadFile("bin/dsky-agent-" + string(arch) + ".exe.gz")
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("agentbin: no agent was embedded for %s (build with `make agent`)", arch)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("agentbin: %s: %w", arch, err)
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// Name is the agent's filename on the media.
const Name = "dsky-agent.exe"
