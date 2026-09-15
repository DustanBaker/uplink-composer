package agent

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// result is what running one program produced.
type result struct {
	Code   int
	Out    string
	Err    error
	Timing time.Duration
}

// ok reports whether the program ran and returned success. It is deliberately
// never the only test of whether a step worked: a driver pack that printed its
// usage text and did nothing also returned 0.
func (r result) ok() bool { return r.Err == nil && r.Code == 0 }

// runner runs a program. It is a variable so tests can watch what the agent
// would run without a Windows machine.
var runner = runReal

func runReal(ctx context.Context, name string, args ...string) result {
	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	r := result{Out: string(out), Timing: time.Since(start)}
	if err != nil {
		var ee *exec.ExitError
		if errorsAs(err, &ee) {
			r.Code = ee.ExitCode()
		} else {
			r.Err = err
			r.Code = -1
		}
	}
	return r
}

// run executes a program with a deadline.
func run(timeout time.Duration, name string, args ...string) result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r := runner(ctx, name, args...)
	if ctx.Err() != nil && r.Err == nil {
		r.Err = ctx.Err()
	}
	return r
}

// trimOut shortens command output for a log line; the full text goes to the
// human log separately.
func trimOut(s string) string {
	s = strings.TrimSpace(s)
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
