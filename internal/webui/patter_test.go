package webui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The line under the progress bar is a script on the real mission clock: max Q
// at T+83, staging at T+2:41, orbit at T+11:42. That only reads as timing if
// the call-outs stay in order and inside the span they are scaled against, and
// a hand-edit that moves one number is silent in a browser — the line simply
// arrives at the wrong moment, or never. So the numbers are checked here.

var phaseRe = regexp.MustCompile(`(?s)(\w+): \{ span: (\d+), pace: (\d+), lines: \[(.*?)\]\s*\},`)
var lineRe = regexp.MustCompile(`\[(\d+), "([^"]+)"\]`)

func patterBlock(t *testing.T) string {
	t.Helper()
	page := string(indexHTML)
	a := strings.Index(page, "const PATTER = {")
	if a < 0 {
		t.Fatal("the page has no PATTER block")
	}
	b := strings.Index(page[a:], "\n};")
	if b < 0 {
		t.Fatal("the PATTER block does not end")
	}
	return page[a : a+b]
}

func TestEveryCallOutIsOnTheClock(t *testing.T) {
	phases := phaseRe.FindAllStringSubmatch(patterBlock(t), -1)
	if len(phases) < 8 {
		t.Fatalf("only found %d phases; the block shape changed", len(phases))
	}
	seen := map[string]bool{}
	for _, p := range phases {
		name, span, pace := p[1], atoi(t, p[2]), atoi(t, p[3])
		seen[name] = true
		if pace <= 0 {
			t.Errorf("%s: pace %d, so a stage with no percentage would never advance", name, pace)
		}
		lines := lineRe.FindAllStringSubmatch(p[4], -1)
		if len(lines) == 0 {
			t.Errorf("%s: no call-outs", name)
			continue
		}
		last := -1
		for _, l := range lines {
			at := atoi(t, l[1])
			if at <= last && !(at == 0 && last == -1) {
				t.Errorf("%s: %q at T+%d comes after T+%d; the script runs forwards only", name, l[2], at, last)
			}
			if at > span {
				t.Errorf("%s: %q at T+%d is past the span of %d and would never be said", name, l[2], at, span)
			}
			last = at
		}
		if first := atoi(t, lines[0][1]); first != 0 {
			t.Errorf("%s: starts at T+%d, so a job has nothing to say at the top of the stage", name, first)
		}
	}
	// Every phase patterPhase can name must exist, or the job falls back to
	// standing by while something is plainly happening.
	for _, want := range []string{"prelaunch", "authority", "fuel", "assembly",
		"launch", "verify", "uplink", "flightplan", "standby"} {
		if !seen[want] {
			t.Errorf("patterPhase can return %q and there is no script for it", want)
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return n
}
