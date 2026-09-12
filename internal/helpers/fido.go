package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DustanBaker/uplink-composer/internal/fetch"
	"github.com/DustanBaker/uplink-composer/internal/manifest"
)

// Fido (github.com/pbatard/Fido, GPLv3, by the Rufus author) resolves
// Microsoft's ephemeral consumer-ISO download URLs. It is fetched once as a
// hash-pinned helper and always run as a subprocess.
const (
	fidoVersion = "v1.70"
	fidoURL     = "https://raw.githubusercontent.com/pbatard/Fido/" + fidoVersion + "/Fido.ps1"
	fidoSHA256  = "24c86067fa399d2fd75ef0693a2ec79ca8db162827f808caac03541cbf640c13"
)

// EnsureFido downloads and verifies the pinned Fido script if it is not
// cached yet, returning its path.
func EnsureFido(ctx context.Context, helpersDir string) (string, error) {
	dir := filepath.Join(helpersDir, "fido")
	path := filepath.Join(dir, "Fido-"+fidoVersion+".ps1")
	if sum, err := fetch.SHA256File(path); err == nil {
		if sum == fidoSHA256 {
			return path, nil
		}
		os.Remove(path) // corrupt or tampered — refetch
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum, err := fetch.Download(ctx, fidoURL, path, nil)
	if err != nil {
		return "", fmt.Errorf("fetching Fido helper: %w", err)
	}
	if sum != fidoSHA256 {
		os.Remove(path)
		return "", fmt.Errorf("Fido helper hash mismatch: got %s, pinned %s — refusing to run it", sum, fidoSHA256)
	}
	return path, nil
}

// powershellBinary picks the host's PowerShell (Windows PowerShell on
// Windows; pwsh 7+ elsewhere).
func powershellBinary() (string, error) {
	if runtime.GOOS == "windows" {
		return "powershell", nil
	}
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("pwsh (PowerShell 7+) is required to resolve Microsoft ISO URLs on this OS — https://aka.ms/powershell")
}

// ResolveFidoURL runs Fido -GetUrl for the spec and returns the ephemeral
// Microsoft download URL (valid for roughly 24 hours).
func ResolveFidoURL(ctx context.Context, helpersDir string, spec *manifest.FidoSpec) (string, error) {
	script, err := EnsureFido(ctx, helpersDir)
	if err != nil {
		return "", err
	}
	ps, err := powershellBinary()
	if err != nil {
		return "", err
	}
	s := manifest.FidoSpec{Win: "11", Release: "Latest", Edition: "Pro", Language: "English", Arch: "x64"}
	if spec != nil {
		if spec.Win != "" {
			s.Win = spec.Win
		}
		if spec.Release != "" {
			s.Release = spec.Release
		}
		if spec.Edition != "" {
			s.Edition = spec.Edition
		}
		if spec.Language != "" {
			s.Language = spec.Language
		}
		if spec.Arch != "" {
			s.Arch = spec.Arch
		}
	}
	// Microsoft URLs stay valid ~24h and their rate limiter ("Sentinel")
	// blocks an IP after only a few link requests — so never ask twice for
	// the same thing while a resolved URL is still fresh.
	cacheKey := fmt.Sprintf("%s|%s|%s|%s|%s", s.Win, s.Release, s.Edition, s.Language, s.Arch)
	cachePath := filepath.Join(helpersDir, "fido", "urlcache.json")
	if url := cachedURL(cachePath, cacheKey); url != "" {
		return url, nil
	}

	cmd := exec.CommandContext(ctx, ps,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", script,
		"-Win", s.Win, "-Rel", s.Release, "-Ed", s.Edition,
		"-Lang", s.Language, "-Arch", s.Arch, "-GetUrl")
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if ee, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(detail + "\n" + strings.TrimSpace(string(ee.Stderr)))
		}
		if strings.Contains(detail, "Sentinel") {
			return "", fmt.Errorf("Microsoft's rate limiter rejected this IP for roughly 24 hours (\"%s\").\nOptions: retry tomorrow; use another network/VPN; or download the ISO in a browser from microsoft.com/software-download/windows11 and run `uplink sources import <id> <path-to.iso>`", detail)
		}
		return "", fmt.Errorf("Fido could not resolve a download URL: %w\n%s", err, detail)
	}
	url := strings.TrimSpace(string(out))
	if !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("Fido returned no URL: %s", url)
	}
	saveCachedURL(cachePath, cacheKey, url)
	return url, nil
}

type fidoCacheEntry struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
}

// cachedURL returns a still-fresh previously resolved URL for key, if any.
func cachedURL(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m map[string]fidoCacheEntry
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	e, ok := m[key]
	if !ok || time.Since(e.FetchedAt) > 20*time.Hour {
		return ""
	}
	return e.URL
}

func saveCachedURL(path, key, url string) {
	m := map[string]fidoCacheEntry{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	m[key] = fidoCacheEntry{URL: url, FetchedAt: time.Now().UTC()}
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}
