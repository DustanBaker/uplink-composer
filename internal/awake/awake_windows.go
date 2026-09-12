package awake

import (
	"runtime"
	"sync"
	"syscall"
)

// Flags for SetThreadExecutionState. ES_DISPLAY_REQUIRED is deliberately not
// used: the screen may sleep, the machine may not.
const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

var setThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").
	NewProc("SetThreadExecutionState")

// keep holds the request on one pinned OS thread.
//
// SetThreadExecutionState is per-thread, and ES_CONTINUOUS stands until that
// same thread clears it. A goroutine can be moved between OS threads at any
// time, so without LockOSThread the request would be left behind on whatever
// thread happened to run the call — still set, on a thread that goes on to do
// something else, and never cleared. Hence a dedicated locked goroutine that
// lives exactly as long as the request.
func keep(string) func() {
	done := make(chan struct{})
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		setThreadExecutionState.Call(uintptr(uint32(esContinuous | esSystemRequired)))
		close(ready)
		<-done
		// ES_CONTINUOUS alone clears the standing request.
		setThreadExecutionState.Call(uintptr(uint32(esContinuous)))
	}()
	<-ready
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}
