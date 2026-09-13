package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// One portal at a time.
//
// The server binds the next free port when one is busy, which means launching
// it twice does not collide — it quietly gives you a second instance on a
// different port. Do that a few times and you have several, each with its own
// token, and the one you are looking at is not necessarily the one you just
// started. That is how a stale portal ends up showing an empty library while
// the real one holds your artifacts.
//
// So a running portal records where it is, and the next launch joins it
// instead of starting another.

// instance is what a running portal leaves behind so the next launch can find
// it. The token is in here because the URL is useless without one.
type instance struct {
	Port    int    `json:"port"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Started string `json:"started"`
}

func (i instance) url() string {
	return fmt.Sprintf("http://127.0.0.1:%d/#t=%s", i.Port, i.Token)
}

// instancePath keeps the record beside the library, which is the one directory
// every copy of this program agrees on.
func instancePath(libRoot string) string {
	return filepath.Join(libRoot, "portal.json")
}

// liveInstance returns the portal already running, or false.
//
// The file alone proves nothing — a crash or a kill leaves it behind — so the
// recorded port is asked whether it is really our portal, with the recorded
// token. Anything else answering on that port is not, and a stale file is
// simply ignored and overwritten.
func liveInstance(libRoot string) (instance, bool) {
	b, err := os.ReadFile(instancePath(libRoot))
	if err != nil {
		return instance{}, false
	}
	var inst instance
	if err := json.Unmarshal(b, &inst); err != nil || inst.Port == 0 || inst.Token == "" {
		return instance{}, false
	}
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/api/state", inst.Port), nil)
	if err != nil {
		return instance{}, false
	}
	req.Header.Set("X-Bootwright-Token", inst.Token)
	// Short: this is a loopback call to a process that is either there or not.
	resp, err := (&http.Client{Timeout: 1500 * time.Millisecond}).Do(req)
	if err != nil {
		return instance{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return instance{}, false
	}
	return inst, true
}

func writeInstance(libRoot string, inst instance) {
	b, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(instancePath(libRoot), b, 0o644)
}

// clearInstance removes our own record on the way out. Best-effort: a stale
// file is harmless because liveInstance probes before trusting it.
func clearInstance(libRoot string, inst instance) {
	if cur, err := os.ReadFile(instancePath(libRoot)); err == nil {
		var on instance
		// Only remove the file if it is still ours — a portal that replaced
		// us must keep its own record.
		if json.Unmarshal(cur, &on) == nil && on.PID != inst.PID {
			return
		}
	}
	_ = os.Remove(instancePath(libRoot))
}
