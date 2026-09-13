//go:build !windows

package cli

// showWindow has no window to give on this platform, so the caller opens the
// portal in a browser instead.
//
// macOS and Linux have their own system web views, but every Go binding for
// them needs a C compiler, which would cost the CGO-free single-binary build
// on every platform to gain a window frame on two. The browser is the honest
// answer there until that trade changes.
func showWindow(url, title string) bool { return false }

// focusWindow has nothing to raise where there is no window.
func focusWindow(title string) bool { return false }
