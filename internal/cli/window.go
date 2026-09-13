package cli

import "sync"

// The window, as far as the rest of the program is concerned: something that
// can be asked to close from anywhere.
//
// Only the updater needs this, and only because restarting means closing a
// window from an HTTP handler rather than from the person who opened it. It
// lives here rather than in the platform file so the caller does not have to
// care whether this build has a window at all.
var (
	winMu        sync.Mutex
	winTerminate func()
)

func setWindowCloser(f func()) {
	winMu.Lock()
	winTerminate = f
	winMu.Unlock()
}

// closeWindow asks the window to close, if there is one. Safe from any
// goroutine; a no-op when the portal is running in a browser instead.
func closeWindow() {
	winMu.Lock()
	f := winTerminate
	winMu.Unlock()
	if f != nil {
		f()
	}
}
