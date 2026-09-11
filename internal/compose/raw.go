package compose

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/DustanBaker/the-composer/internal/buildinfo"
)

// buildRaw handles linux-iso and raw-img recipes: the artifact IS the blob
// (no copy); compression is detected by magic bytes and recorded so the
// flash engine can stream-decompress.
func buildRaw(_ context.Context, req Request) (*Artifact, error) {
	r := req.Recipe
	src, err := req.Workspace.Source(r.OS.Source)
	if err != nil {
		return nil, err
	}
	entry, err := req.Library.Resolve(src.ID)
	if err != nil {
		return nil, err
	}
	blob := req.Library.BlobPath(entry.SHA256)
	comp, err := DetectCompression(blob)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(blob)
	if err != nil {
		return nil, err
	}
	key, err := inputsKey(req, entry.SHA256)
	if err != nil {
		return nil, err
	}
	a := &Artifact{
		RecipeID: r.ID, Kind: "raw", Path: blob, Size: st.Size(),
		SHA256: entry.SHA256, Compress: comp, InputsKey: key,
		Verify: r.Flash.Verify, MinStick: minStickBytes(r),
		CreatedAt: nowUTC(), Tool: toolVersion(),
	}
	// Raw artifacts get no sidecar next to the blob (blobs are content-
	// addressed and shared); the caller passes the Artifact straight to
	// flash.
	return a, nil
}

// DetectCompression sniffs xz/zstd/gzip magic bytes.
func DetectCompression(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var magic [6]byte
	n, err := f.Read(magic[:])
	if err != nil || n < 4 {
		return "", fmt.Errorf("reading magic of %s: %w", path, err)
	}
	switch {
	case magic[0] == 0xFD && magic[1] == '7' && magic[2] == 'z' && magic[3] == 'X' && magic[4] == 'Z':
		return "xz", nil
	case magic[0] == 0x28 && magic[1] == 0xB5 && magic[2] == 0x2F && magic[3] == 0xFD:
		return "zstd", nil
	case magic[0] == 0x1F && magic[1] == 0x8B:
		return "gz", nil
	default:
		return "none", nil
	}
}

func nowUTC() time.Time     { return time.Now().UTC() }
func toolVersion() string   { return buildinfo.Version }
