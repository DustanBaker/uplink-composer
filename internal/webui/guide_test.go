package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/uplinkresearch/dsky/internal/appconfig"
)

// Each guide — the start screen's and one per screen — is offered until it
// has been closed once, remembered in the user's settings file so it survives
// restarts and a different window or browser profile.
func TestGuidesAreRememberedOneByOne(t *testing.T) {
	useTempConfig(t)
	s := testServer(t)
	s.Cfg = appconfig.Load()
	h := s.handler()

	if seen := seenGuides(t, h); len(seen) != 0 {
		t.Fatalf("a fresh install has seen guides: %v", seen)
	}
	for _, g := range []string{"home", "install"} {
		if w := post(t, h, "/api/guide", `{"guide":"`+g+`","seen":true}`); w.Code != 200 {
			t.Fatalf("mark %s: %d %s", g, w.Code, w.Body)
		}
	}
	if seen := seenGuides(t, h); !slices.Contains(seen, "home") || !slices.Contains(seen, "install") || slices.Contains(seen, "clone") {
		t.Errorf("seen guides: %v", seen)
	}
	// A new process reads them back from the settings file.
	if got := appconfig.Load().SeenGuides(); !slices.Contains(got, "install") {
		b, _ := os.ReadFile(filepath.Join(appconfig.Dir(), "config.json"))
		t.Errorf("settings file does not remember them: %s", b)
	}
	// One guide forgotten, then all of them.
	post(t, h, "/api/guide", `{"guide":"install","seen":false}`)
	if seen := seenGuides(t, h); slices.Contains(seen, "install") || !slices.Contains(seen, "home") {
		t.Errorf("after forgetting install: %v", seen)
	}
	post(t, h, "/api/guide", `{"seen":false}`)
	if seen := seenGuides(t, h); len(seen) != 0 {
		t.Errorf("after forgetting all: %v", seen)
	}
	for _, bad := range []string{`nonsense`, `{"guide":"../etc","seen":true}`, `{"seen":true}`} {
		if w := post(t, h, "/api/guide", bad); w.Code != 400 {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
}

// v0.7.5 had one guide and one flag for it. Updating must not replay it.
func TestGuideFlagFromV075CountsAsHome(t *testing.T) {
	useTempConfig(t)
	if err := os.MkdirAll(appconfig.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appconfig.Dir(), "config.json"), []byte(`{"guide_seen": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	s.Cfg = appconfig.Load()
	if seen := seenGuides(t, s.handler()); !slices.Equal(seen, []string{"home"}) {
		t.Errorf("a v0.7.5 settings file gives %v, want [home]", seen)
	}
}

// Without a settings file the guides are remembered for the run instead.
func TestGuidesWithoutSettings(t *testing.T) {
	s := testServer(t)
	h := s.handler()
	if w := post(t, h, "/api/guide", `{"guide":"disks","seen":true}`); w.Code != 200 || !slices.Contains(s.seenGuides(), "disks") {
		t.Errorf("no settings: %d %v", w.Code, s.seenGuides())
	}
}

func useTempConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
}

func seenGuides(t *testing.T, h http.Handler) []string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Host = "127.0.0.1:8931"
	req.Header.Set("X-DSKY-Token", "sekrit")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var st struct {
		GuidesSeen []string `json:"guides_seen"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.GuidesSeen == nil {
		t.Fatalf("state has no guides_seen: %s", w.Body)
	}
	return st.GuidesSeen
}
