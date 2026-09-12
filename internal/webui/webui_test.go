package webui

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/DustanBaker/uplink-composer/internal/jobs"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/workspace"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	wsDir := filepath.Join(root, "ws")
	if err := workspace.Scaffold(wsDir, "Test Org"); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(filepath.Join(root, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	// Cfg left nil so the test never writes to the real user config dir.
	s := &Server{Lib: lib, Token: "sekrit", Reg: jobs.NewRegistry()}
	if err := s.SetWorkspaceDir(wsDir); err != nil {
		t.Fatal(err)
	}
	return s
}

func do(t *testing.T, h http.Handler, method, path, host, origin, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("X-Uplink-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAuthAndGuards(t *testing.T) {
	h := testServer(t).handler()

	// The page itself needs no token but a loopback Host.
	if w := do(t, h, "GET", "/", "127.0.0.1:8931", "", ""); w.Code != 200 {
		t.Errorf("index: %d", w.Code)
	}
	// DNS rebinding: non-loopback Host is refused everywhere.
	if w := do(t, h, "GET", "/", "evil.example:8931", "", ""); w.Code != http.StatusForbidden {
		t.Errorf("rebound host: got %d, want 403", w.Code)
	}
	// API without token: 401.
	if w := do(t, h, "GET", "/api/state", "localhost:8931", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", w.Code)
	}
	// API with wrong token: 401.
	if w := do(t, h, "GET", "/api/state", "localhost:8931", "", "wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("bad token: got %d, want 401", w.Code)
	}
	// API with token: 200.
	if w := do(t, h, "GET", "/api/state", "localhost:8931", "", "sekrit"); w.Code != 200 {
		t.Errorf("state: got %d body %s", w.Code, w.Body.String())
	}
	// Cross-origin POST is refused even with the token.
	if w := do(t, h, "POST", "/api/build", "127.0.0.1:8931", "https://evil.example", "sekrit"); w.Code != http.StatusForbidden {
		t.Errorf("cross-origin post: got %d, want 403", w.Code)
	}
	// Same-origin POST passes the guard (fails later on the empty body).
	if w := do(t, h, "POST", "/api/build", "127.0.0.1:8931", "http://127.0.0.1:8931", "sekrit"); w.Code != http.StatusBadRequest {
		t.Errorf("same-origin post: got %d, want 400", w.Code)
	}
}

func TestFlashRefusesBadConfirm(t *testing.T) {
	s := testServer(t)
	h := s.handler()
	req := httptest.NewRequest("POST", "/api/flash", nil)
	req.Host = "127.0.0.1:8931"
	req.Header.Set("X-Uplink-Token", "sekrit")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty flash: got %d, want 400", w.Code)
	}
}
