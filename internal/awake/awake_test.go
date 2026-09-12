package awake

import "testing"

// TestKeepReleaseIsIdempotent: release is meant to be deferred, and a deferred
// release sitting beside an explicit one on an error path must not panic on a
// closed channel — which is exactly what a second close() would do.
func TestKeepReleaseIsIdempotent(t *testing.T) {
	release := Keep("test")
	if release == nil {
		t.Fatal("Keep returned no release function")
	}
	release()
	release()
	release()
}

// TestKeepAlwaysReturnsSomething: no platform may return a nil release, and
// none may fail. Being unable to inhibit sleep is not a reason to refuse to
// write a stick, so every path degrades to a no-op instead of an error.
func TestKeepAlwaysReturnsSomething(t *testing.T) {
	for _, reason := range []string{"", "writing removable media"} {
		if r := Keep(reason); r == nil {
			t.Errorf("Keep(%q) returned nil", reason)
		} else {
			r()
		}
	}
}
