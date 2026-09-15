//go:build !windows

package agent

import "errors"

// There is no window anywhere but Windows, which is the only place the agent
// provisions a machine. openScreen turns this into a nil screen, and every
// call on it does nothing.
func newWindow(s *screen) (window, error) {
	return nil, errors.New("the status window is a Windows feature")
}
