package cli

import (
	"sync"
	"testing"
	"time"
)

// themeProbe is a stand-in for the registry read and the DWM call, so the
// follow logic can be tested without touching the machine's actual theme —
// which would re-colour every window on the desktop of whoever ran the tests.
type themeProbe struct {
	mu      sync.Mutex
	dark    bool
	applied []bool
}

func (p *themeProbe) read() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dark
}

func (p *themeProbe) set(dark bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dark = dark
}

func (p *themeProbe) apply(dark bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.applied = append(p.applied, dark)
}

func (p *themeProbe) calls() []bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]bool(nil), p.applied...)
}

// TestPaintsTheTitleBarBeforeTheWindowIsShown: the first apply has to happen
// on the caller's goroutine. Deferring it to the ticker races the window
// appearing, and losing that race is a light title bar that snaps dark a
// moment later — the exact flicker this is meant to remove.
func TestPaintsTheTitleBarBeforeTheWindowIsShown(t *testing.T) {
	p := &themeProbe{dark: true}
	stop := watchTheme(p.read, p.apply, time.Hour)
	defer stop()

	got := p.calls()
	if len(got) != 1 || !got[0] {
		t.Fatalf("wanted one immediate dark apply, got %v", got)
	}
}

// TestFollowsTheSystemFlippingToLight is the feature: change Windows' theme
// while the window is open and the frame follows, rather than staying
// conspicuously wrong until the app is restarted.
func TestFollowsTheSystemFlippingToLight(t *testing.T) {
	p := &themeProbe{dark: true}
	stop := watchTheme(p.read, p.apply, 5*time.Millisecond)
	defer stop()

	p.set(false)
	waitFor(t, func() bool {
		c := p.calls()
		return len(c) == 2 && c[0] && !c[1]
	}, "the title bar did not follow the system into light mode")
}

// TestDoesNotRepaintWhenNothingChanged: the ticker keeps firing for as long as
// the window is open. Calling into DWM every couple of seconds regardless
// would be pointless work on every machine that never touches its theme.
func TestDoesNotRepaintWhenNothingChanged(t *testing.T) {
	p := &themeProbe{dark: true}
	stop := watchTheme(p.read, p.apply, 5*time.Millisecond)
	defer stop()

	time.Sleep(100 * time.Millisecond) // ~20 ticks, none of them a change
	if got := p.calls(); len(got) != 1 {
		t.Errorf("wanted only the initial apply, got %d: %v", len(got), got)
	}
}

// TestStopsWatching: the watcher must not outlive the window it paints.
func TestStopsWatching(t *testing.T) {
	p := &themeProbe{dark: true}
	stop := watchTheme(p.read, p.apply, 5*time.Millisecond)
	stop()
	stop() // idempotent: showWindow defers it, and it is cheap to call twice

	p.set(false)
	time.Sleep(60 * time.Millisecond)
	if got := p.calls(); len(got) != 1 {
		t.Errorf("kept painting after being stopped: %v", got)
	}
}

func waitFor(t *testing.T, ok func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}
