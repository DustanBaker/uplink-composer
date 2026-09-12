// Package library is the machine-local content store: a content-addressed
// blob store plus a catalog mapping source IDs to blobs. Multi-gigabyte
// binaries live here (never in git); workspaces reference them by manifest.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/DustanBaker/uplink-composer/internal/fetch"
	"github.com/DustanBaker/uplink-composer/internal/manifest"
)

// Entry is one catalog record.
type Entry struct {
	ID         string          `json:"id"`
	SHA256     string          `json:"sha256"`
	Filename   string          `json:"filename"`
	Kind       manifest.Kind   `json:"kind"`
	Format     manifest.Format `json:"format"`
	Size       int64           `json:"size"`
	SourceURL  string          `json:"source_url,omitempty"`
	ImportedAt time.Time       `json:"imported_at"`
}

// Library is a store rooted at one directory.
type Library struct {
	Root string
}

// DefaultRoot picks the per-user store location: never a roaming or
// cloud-synced path (multi-GB blobs).
func DefaultRoot() string {
	if env := os.Getenv("UPLINK_LIBRARY"); env != "" {
		return env
	}
	switch runtime.GOOS {
	case "windows":
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, "uplink-composer")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "uplink-composer")
		}
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "uplink-composer")
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "uplink-composer")
		}
	}
	return filepath.Join(".", "uplink-composer-library")
}

// Open ensures the directory layout exists and returns the library. A
// library left under the tool's former name is migrated once, so an
// existing cache of ISOs and driver packs survives the rename.
func Open(root string) (*Library, error) {
	if filepath.Base(root) == "uplink-composer" {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			old := filepath.Join(filepath.Dir(root), "the-composer")
			if st, err := os.Stat(old); err == nil && st.IsDir() {
				_ = os.Rename(old, root) // best-effort; falls through to a fresh dir on failure
			}
		}
	}
	l := &Library{Root: root}
	for _, d := range []string{l.blobDir(), l.TmpDir(), l.ArtifactsDir(), l.HelpersDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func (l *Library) blobDir() string      { return filepath.Join(l.Root, "blobs", "sha256") }
func (l *Library) catalogPath() string  { return filepath.Join(l.Root, "catalog.json") }

// TmpDir holds in-progress downloads and staging trees; same volume as blobs
// so finalizing is a rename.
func (l *Library) TmpDir() string { return filepath.Join(l.Root, "tmp") }

// ArtifactsDir holds composed images.
func (l *Library) ArtifactsDir() string { return filepath.Join(l.Root, "artifacts") }

// HelpersDir holds pinned helper binaries (7zz on Linux, wimlib, ...).
func (l *Library) HelpersDir() string { return filepath.Join(l.Root, "helpers") }

// BlobPath is where content with this hash lives.
func (l *Library) BlobPath(sha256 string) string {
	return filepath.Join(l.blobDir(), sha256[:2], sha256)
}

func (l *Library) loadCatalog() (map[string]Entry, error) {
	b, err := os.ReadFile(l.catalogPath())
	if os.IsNotExist(err) {
		return map[string]Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]Entry
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("library catalog %s is corrupt: %w", l.catalogPath(), err)
	}
	return m, nil
}

func (l *Library) saveCatalog(m map[string]Entry) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.catalogPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Remove(l.catalogPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmp, l.catalogPath())
}

// storeBlob moves a fully-verified file into the blob store.
func (l *Library) storeBlob(path, sha string) error {
	dst := l.BlobPath(sha)
	if _, err := os.Stat(dst); err == nil {
		return os.Remove(path) // already have it
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(path, dst); err != nil {
		// Cross-volume import: fall back to copy.
		if err := copyFile(path, dst); err != nil {
			return err
		}
		return os.Remove(path)
	}
	return nil
}

// Import copies a local file (e.g. a manually downloaded Windows ISO) into
// the store under the given source metadata and returns its entry.
func (l *Library) Import(src *manifest.Source, filePath string) (Entry, error) {
	sum, err := fetch.SHA256File(filePath)
	if err != nil {
		return Entry{}, err
	}
	if src.SHA256 != "" && src.SHA256 != sum {
		return Entry{}, fmt.Errorf("library: %s has sha256 %s, manifest %s pins %s — wrong file or corrupted download",
			filePath, sum, src.ID, src.SHA256)
	}
	st, err := os.Stat(filePath)
	if err != nil {
		return Entry{}, err
	}
	// Copy into tmp first so the original file is never consumed.
	tmp := filepath.Join(l.TmpDir(), "import-"+sum)
	if err := copyFile(filePath, tmp); err != nil {
		return Entry{}, err
	}
	if err := l.storeBlob(tmp, sum); err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID: src.ID, SHA256: sum, Filename: filepath.Base(filePath),
		Kind: src.Kind, Format: src.Format, Size: st.Size(),
		ImportedAt: time.Now().UTC(),
	}
	if src.Filename != "" {
		e.Filename = src.Filename
	}
	return e, l.record(e)
}

// ErrUnpinned is returned by Pull for a manifest without a sha256 pin when
// trust-on-first-use was not explicitly requested.
type ErrUnpinned struct {
	ID     string
	SHA256 string // computed hash of what was downloaded
}

func (e *ErrUnpinned) Error() string {
	return fmt.Sprintf("source %s has no sha256 pin; downloaded content hashes to %s — verify it, add `sha256: %s` to the manifest, or re-run with --pin-tofu",
		e.ID, e.SHA256, e.SHA256)
}

// URLResolver resolves a provider-based source (e.g. fido) to a concrete
// download URL at pull time.
type URLResolver func(ctx context.Context, src *manifest.Source) (string, error)

// Pull downloads a manifest source into the store. Unpinned sources download
// but return *ErrUnpinned unless pinTOFU is set; either way the computed hash
// is preserved (the blob stays cached), so pinning then re-pulling is free.
// resolve is consulted for provider-based sources; nil restricts Pull to
// plain-URL manifests.
func (l *Library) Pull(ctx context.Context, src *manifest.Source, pinTOFU bool, resolve URLResolver, progress fetch.Progress) (Entry, error) {
	if e, err := l.Resolve(src.ID); err == nil && src.SHA256 != "" && e.SHA256 == src.SHA256 {
		return e, nil // already present and matching
	}
	url := src.URL
	if src.Provider != "" {
		if resolve == nil {
			return Entry{}, fmt.Errorf("library: source %s uses provider %s, which this caller cannot resolve", src.ID, src.Provider)
		}
		var err error
		if url, err = resolve(ctx, src); err != nil {
			return Entry{}, err
		}
	}
	if url == "" {
		return Entry{}, fmt.Errorf("library: source %s has no url; use `uplink sources import %s <file>`", src.ID, src.ID)
	}
	dest := filepath.Join(l.TmpDir(), src.ID+"-"+src.DownloadFilename())
	sum, err := fetch.Download(ctx, url, dest, progress)
	if err != nil {
		return Entry{}, err
	}
	if src.SHA256 != "" && sum != src.SHA256 {
		os.Remove(dest)
		return Entry{}, fmt.Errorf("library: %s downloaded from %s hashes to %s, manifest pins %s — refusing (upstream changed or download corrupted)",
			src.ID, url, sum, src.SHA256)
	}
	st, _ := os.Stat(dest)
	var size int64
	if st != nil {
		size = st.Size()
	}
	if err := l.storeBlob(dest, sum); err != nil {
		return Entry{}, err
	}
	filename := src.DownloadFilename()
	if src.Filename == "" && src.Provider != "" {
		// Providers resolve the real name at pull time (e.g. the ISO name
		// Microsoft serves); prefer it over the manifest id.
		if base := urlBasename(url); base != "" {
			filename = base
		}
	}
	e := Entry{
		ID: src.ID, SHA256: sum, Filename: filename,
		Kind: src.Kind, Format: src.Format, Size: size,
		SourceURL: url, ImportedAt: time.Now().UTC(),
	}
	if src.SHA256 == "" && !pinTOFU {
		// Blob is stored (cache) but not cataloged as trusted.
		return Entry{}, &ErrUnpinned{ID: src.ID, SHA256: sum}
	}
	return e, l.record(e)
}

func (l *Library) record(e Entry) error {
	cat, err := l.loadCatalog()
	if err != nil {
		return err
	}
	cat[e.ID] = e
	return l.saveCatalog(cat)
}

// Resolve returns the catalog entry for id; the blob is verified to exist.
func (l *Library) Resolve(id string) (Entry, error) {
	cat, err := l.loadCatalog()
	if err != nil {
		return Entry{}, err
	}
	e, ok := cat[id]
	if !ok {
		return Entry{}, fmt.Errorf("library: %s is not in the local library — run `uplink sources pull %s` or `uplink sources import %s <file>`", id, id, id)
	}
	if _, err := os.Stat(l.BlobPath(e.SHA256)); err != nil {
		return Entry{}, fmt.Errorf("library: %s is cataloged but its blob is missing (%s) — re-pull or re-import", id, l.BlobPath(e.SHA256))
	}
	return e, nil
}

// List returns catalog entries sorted by ID.
func (l *Library) List() ([]Entry, error) {
	cat, err := l.loadCatalog()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(cat))
	for _, e := range cat {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GC removes blobs no catalog entry references and clears tmp. Returns bytes
// freed.
func (l *Library) GC() (int64, error) {
	cat, err := l.loadCatalog()
	if err != nil {
		return 0, err
	}
	wanted := map[string]bool{}
	for _, e := range cat {
		wanted[strings.ToLower(e.SHA256)] = true
	}
	var freed int64
	err = filepath.WalkDir(l.blobDir(), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !wanted[strings.ToLower(d.Name())] {
			if st, err := os.Stat(p); err == nil {
				freed += st.Size()
			}
			return os.Remove(p)
		}
		return nil
	})
	if err != nil {
		return freed, err
	}
	entries, err := os.ReadDir(l.TmpDir())
	if err != nil {
		return freed, err
	}
	for _, e := range entries {
		p := filepath.Join(l.TmpDir(), e.Name())
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			freed += st.Size()
		}
		os.RemoveAll(p)
	}
	return freed, nil
}

// urlBasename extracts the final path segment of a URL, without query.
func urlBasename(u string) string {
	base := u[strings.LastIndex(u, "/")+1:]
	if i := strings.IndexAny(base, "?#"); i >= 0 {
		base = base[:i]
	}
	return base
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
