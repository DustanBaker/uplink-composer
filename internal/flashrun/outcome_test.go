package flashrun

import "testing"

func TestWorkerOutcome(t *testing.T) {
	if r, err := workerOutcome(0, "prepared disk2", ""); err != nil || r != "prepared disk2" {
		t.Fatalf("finished job: %q, %v", r, err)
	}
	// The Windows app relaunched as administrator used to raise its window
	// and exit 0 without running the job; that must be a failure.
	if _, err := workerOutcome(0, "", ""); err == nil {
		t.Fatal("a clean exit with no result was reported as done")
	}
	if _, err := workerOutcome(1, "", "diskpart reported a problem"); err == nil || err.Error() != "diskpart reported a problem" {
		t.Fatalf("worker error: %v", err)
	}
}
