package webui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uplinkresearch/dsky/internal/appconfig"
)

// The first-run guide is offered until it has been closed once, and that is
// remembered in the user's settings file so it survives restarts and a
// different window or browser profile.
func TestGuideSeenIsRemembered(t *testing.T) {
	cfgHome := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", cfgHome)
	case "darwin":
		t.Setenv("HOME", cfgHome)
	default:
		t.Setenv("XDG_CONFIG_HOME", cfgHome)
	}

	s := testServer(t)
	s.Cfg = appconfig.Load()
	h := s.handler()

	seen := func() bool {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/state", nil)
		req.Host = "127.0.0.1:8931"
		req.Header.Set("X-DSKY-Token", "sekrit")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var st struct {
			GuideSeen *bool `json:"guide_seen"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.GuideSeen == nil {
			t.Fatalf("state has no guide_seen: %s", w.Body)
		}
		return *st.GuideSeen
	}

	if seen() {
		t.Fatal("a fresh install already counts the guide as seen")
	}
	if w := post(t, h, "/api/guide", `{"seen":true}`); w.Code != 200 {
		t.Fatalf("mark seen: %d %s", w.Code, w.Body)
	}
	if !seen() {
		t.Error("state does not report the guide as seen")
	}
	// A new process reads it back from the settings file.
	b, err := os.ReadFile(filepath.Join(appconfig.Dir(), "config.json"))
	if err != nil {
		t.Fatalf("settings file: %v", err)
	}
	if !appconfig.Load().Seen() {
		t.Errorf("settings file does not remember it: %s", b)
	}
	if w := post(t, h, "/api/guide", `{"seen":false}`); w.Code != 200 || seen() {
		t.Error("the guide could not be offered again")
	}
	if w := post(t, h, "/api/guide", `nonsense`); w.Code != 400 {
		t.Errorf("bad body: %d", w.Code)
	}
}

// Without a settings file (a portal started for tests or scripting) the guide
// is remembered for the run instead of failing.
func TestGuideSeenWithoutSettings(t *testing.T) {
	s := testServer(t)
	h := s.handler()
	if w := post(t, h, "/api/guide", `{"seen":true}`); w.Code != 200 || !s.guideSeen() {
		t.Errorf("no settings: %d seen=%v", w.Code, s.guideSeen())
	}
}
