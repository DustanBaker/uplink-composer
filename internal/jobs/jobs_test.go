package jobs

import "testing"

// TestBusyExceptIgnoresTheAsker is the bug this exists for: the self-update
// guarded on Busy so it would not restart on top of another job, and a job is
// always unfinished from inside itself — so it guarded on its own existence
// and silently never restarted. Nothing errored; the app simply sat there.
func TestBusyExceptIgnoresTheAsker(t *testing.T) {
	r := NewRegistry()
	self := r.New("update", "updating")
	self.Progress("downloading", 1, 10)

	if !r.Busy() {
		t.Fatal("Busy did not see a running job")
	}
	if r.BusyExcept(self.ID) {
		t.Error("BusyExcept counted the job that asked")
	}

	other := r.New("flash", "writing a stick")
	other.Progress("copy", 1, 10)
	if !r.BusyExcept(self.ID) {
		t.Error("BusyExcept missed a genuinely running peer")
	}

	other.Finish("written")
	if r.BusyExcept(self.ID) {
		t.Error("BusyExcept still sees a peer that finished")
	}
}
