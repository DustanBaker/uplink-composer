// Package selfupdate replaces the running dsky binary with the newest
// published release.
//
// The release assets are plain files on a public GitHub release, so this
// needs no credentials and no update server. Each download is checked
// against the SHA256SUMS.txt published beside it.
//
// That hash is an integrity check, not a signature: it is produced by the
// same release job as the binary, so it proves the file arrived intact, not
// that it came from someone trusted. Transport is HTTPS, which covers the
// network; what it does not cover is a compromised release. Signing the
// sums with a key pinned into this binary is the fix, and the place to add
// it is verify() below.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/uplinkresearch/dsky/internal/buildinfo"
)

// Repo is the published source of releases.
const Repo = "uplinkresearch/dsky"

// Release is a published version and the asset for this platform.
type Release struct {
	Version string // e.g. "v0.2.1"
	Notes   string
	Asset   string // asset filename for this OS/arch
	URL     string
	SumsURL string
	Size    int64
	// Newer reports whether Version is actually ahead of the running build.
	Newer bool
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// AssetName is the release asset for a given version and program. prog is
// "dsky" or "dsky-app" (the windowless launcher).
func AssetName(prog, version, goos, goarch string) string {
	name := fmt.Sprintf("%s-%s-%s-%s", prog, version, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Check asks the release feed what the newest version is. A release with
// Newer false means the running build is already current (or ahead).
func Check(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("checking for updates: HTTP %d", resp.StatusCode)
	}
	var gh ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&gh); err != nil {
		return nil, err
	}
	if gh.TagName == "" {
		return nil, fmt.Errorf("the release feed named no version")
	}

	want := AssetName(programName(), gh.TagName, runtime.GOOS, runtime.GOARCH)
	rel := &Release{
		Version: gh.TagName,
		Notes:   gh.Body,
		Asset:   want,
		Newer:   compareVersions(gh.TagName, buildinfo.Version) > 0,
	}
	for _, a := range gh.Assets {
		switch a.Name {
		case want:
			rel.URL, rel.Size = a.URL, a.Size
		case "SHA256SUMS.txt":
			rel.SumsURL = a.URL
		}
	}
	if rel.URL == "" {
		return nil, fmt.Errorf("release %s has no build for %s/%s (looked for %s)",
			gh.TagName, runtime.GOOS, runtime.GOARCH, want)
	}
	if rel.SumsURL == "" {
		return nil, fmt.Errorf("release %s publishes no SHA256SUMS.txt — refusing to update unverified", gh.TagName)
	}
	return rel, nil
}

// programName is the binary being run, normalised back to its release name,
// so `dsky-app` updates itself rather than pulling down the console build.
func programName() string {
	exe, err := os.Executable()
	if err != nil {
		return "dsky"
	}
	base := strings.ToLower(filepath.Base(exe))
	base = strings.TrimSuffix(base, ".exe")
	if strings.HasPrefix(base, "dsky-app") {
		return "dsky-app"
	}
	return "dsky"
}

// Apply downloads the release asset, verifies it, and swaps it in for the
// running executable. Returns the path replaced.
func Apply(ctx context.Context, rel *Release, progress func(done, total int64)) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)

	// Staged in the destination directory: a cross-volume rename is not
	// atomic, and this has to land in one step.
	tmp, err := os.CreateTemp(dir, ".dsky-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot write to %s — reinstall instead, or run with rights to that directory: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed away

	sum, err := download(ctx, rel.URL, tmp, rel.Size, progress)
	tmp.Close()
	if err != nil {
		return "", err
	}
	if err := verify(ctx, rel, sum); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return "", err
	}

	// A running executable cannot be overwritten on Windows, but it can be
	// renamed out of the way; the old file is removed on a later run.
	backup := exe + ".old"
	os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return "", fmt.Errorf("replacing %s: %w", exe, err)
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		os.Rename(backup, exe) // put it back rather than leave nothing there
		return "", fmt.Errorf("installing the new build: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		// Expected on Windows while the old image is still mapped.
		_ = err
	}
	return exe, nil
}

// CleanupOld removes the previous binary left behind by an update, which
// Windows refuses to delete while it is still running.
func CleanupOld() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}

func download(ctx context.Context, url string, w io.Writer, total int64, progress func(done, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", filepath.Base(url), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("downloading %s: HTTP %d", filepath.Base(url), resp.StatusCode)
	}
	h := sha256.New()
	pw := &progressWriter{w: io.MultiWriter(w, h), total: total, report: progress}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verify checks the downloaded hash against the sums published with the
// release. A mismatch aborts before anything is swapped in.
func verify(ctx context.Context, rel *Release, got string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.SumsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") != rel.Asset {
			continue
		}
		if !strings.EqualFold(fields[0], got) {
			return fmt.Errorf("%s failed verification: got %s, the release says %s — not installing it",
				rel.Asset, got, fields[0])
		}
		return nil
	}
	return fmt.Errorf("%s is not listed in the release checksums — refusing to install it unverified", rel.Asset)
}

type progressWriter struct {
	w      io.Writer
	done   int64
	total  int64
	report func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.report != nil {
		p.report(p.done, p.total)
	}
	return n, err
}

// compareVersions orders two "vX.Y.Z" tags: 1 if a is newer, -1 if older, 0
// if the same. A trailing pre-release suffix (-dev, -rc1) sorts before the
// plain release of the same numbers, so a dev build updates to its release.
func compareVersions(a, b string) int {
	an, apre := splitVersion(a)
	bn, bpre := splitVersion(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		x, y := 0, 0
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "": // a release outranks a pre-release of the same numbers
		return 1
	case bpre == "":
		return -1
	case apre > bpre:
		return 1
	default:
		return -1
	}
}

func splitVersion(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	pre := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre, v = v[i+1:], v[:i]
	}
	var nums []int
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	return nums, pre
}
