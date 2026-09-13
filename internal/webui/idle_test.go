package webui

import (
	"sync"
	"testing"
	"time"

	"github.com/uplinkresearch/bootwright/internal/jobs"
)

// idleServer is a Server wired only for the lifecycle: no library, no
// listener, just the bookkeeping that decides when to stop.
func idleServer(t *testing.T, timeout time.Duration) (*Server, func() bool) {
	t.Helper()
	var mu sync.Mutex
	stopped := false
	s := &Server{
		Reg:         jobs.NewRegistry(),
		IdleTimeout: timeout,
	}
	s.quit = func() {
		mu.Lock()
		stopped = true
		mu.Unlock()
	}
	return s, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return stopped
	}
}

// TestStopsWhenTheLastPageCloses is the whole point: closing the tab is the
// gesture everyone actually uses, and without this it leaks a server every
// time — on a new port each time, so they accumulate instead of colliding.
func TestStopsWhenTheLastPageCloses(t *testing.T) {
	s, stopped := idleServer(t, 30*time.Millisecond)

	s.pageOpened()
	time.Sleep(50 * time.Millisecond)
	if stopped() {
		t.Fatal("stopped while a page was open")
	}

	s.pageClosed()
	time.Sleep(120 * time.Millisecond)
	if !stopped() {
		t.Error("the last page closed and the server kept running")
	}
}

// TestSurvivesAReload: a reload drops the event stream and reopens it a moment
// later. Treating that as leaving would close the portal under someone who is
// still using it.
func TestSurvivesAReload(t *testing.T) {
	s, stopped := idleServer(t, 80*time.Millisecond)
	s.pageOpened()
	s.pageClosed() // the reload
	time.Sleep(20 * time.Millisecond)
	s.pageOpened() // and it is back
	time.Sleep(150 * time.Millisecond)
	if stopped() {
		t.Error("a reload was mistaken for the page closing")
	}
}

// TestTwoTabs: closing one of two tabs is not leaving.
func TestTwoTabs(t *testing.T) {
	s, stopped := idleServer(t, 30*time.Millisecond)
	s.pageOpened()
	s.pageOpened()
	s.pageClosed()
	time.Sleep(120 * time.Millisecond)
	if stopped() {
		t.Fatal("stopped while a second tab was still open")
	}
	s.pageClosed()
	time.Sleep(120 * time.Millisecond)
	if !stopped() {
		t.Error("the last tab closed and the server kept running")
	}
}

// TestNeverStopsMidJob is the one that must not be got wrong. A technician
// starts a flash and closes the tab out of habit; killing the server there
// would abandon a half-written stick, which is the worst thing this tool can
// leave behind.
func TestNeverStopsMidJob(t *testing.T) {
	s, stopped := idleServer(t, 30*time.Millisecond)
	job := s.Reg.New("flash", "writing a stick")

	s.pageOpened()
	s.pageClosed()
	time.Sleep(150 * time.Millisecond)
	if stopped() {
		t.Fatal("stopped mid-flash after the tab was closed")
	}

	// And once the write finishes, it stops without anyone coming back.
	job.Finish("written and verified")
	time.Sleep(150 * time.Millisecond)
	if !stopped() {
		t.Error("the job finished with nobody watching and the server stayed up")
	}
}

// TestNeverStopsBeforeTheFirstPage: the browser can take a second to launch,
// and the server starts with nobody connected. Counting that as idle would
// have it exit before the page it just opened ever arrives.
func TestNeverStopsBeforeTheFirstPage(t *testing.T) {
	_, stopped := idleServer(t, 20*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	if stopped() {
		t.Error("stopped before any page had connected")
	}
}

// TestKeepAliveStaysUp: zero means somebody wants it running on purpose.
func TestKeepAliveStaysUp(t *testing.T) {
	s, stopped := idleServer(t, 0)
	s.pageOpened()
	s.pageClosed()
	time.Sleep(120 * time.Millisecond)
	if stopped() {
		t.Error("stopped despite no idle timeout being set")
	}
}
