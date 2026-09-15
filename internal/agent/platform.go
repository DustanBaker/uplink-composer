package agent

import (
	"errors"
	"os/exec"
)

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func lookPath(name string) (string, error) { return exec.LookPath(name) }
