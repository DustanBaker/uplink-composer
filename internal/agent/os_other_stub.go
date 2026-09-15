//go:build !windows

package agent

import "errors"

// RunUserJob is the unelevated half of an install; Windows-only.
func RunUserJob(jobPath, resultPath string) error {
	return errors.New("standard-user installs are a Windows feature")
}
