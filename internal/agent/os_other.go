//go:build !windows

package agent

import "errors"

// The agent only ever runs on Windows. These stubs exist so the package
// builds and its logic can be tested on the machine DSKY is developed on.

func (a *Agent) machineModel() (vendor, model string) { return "", "" }

func (a *Agent) runAsSignedInUser(exe string, args []string) (int, error) {
	return 0, errors.New("standard-user installs are a Windows feature")
}

func (a *Agent) setPolicy(p policy) error {
	return errors.New("the registry is a Windows feature")
}

func (a *Agent) removeAppx(prefixes []string) {
	a.J.Info(stepDebloat, "not running on Windows, nothing to remove")
}
