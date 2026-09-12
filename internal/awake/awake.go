// Package awake keeps the machine from sleeping while a long write runs.
//
// Writing and verifying a stick takes minutes, and a laptop that suspends
// partway through leaves a half-written device. That failure is worse than it
// sounds: the write is interrupted after the partition table has landed, so
// the stick mounts, looks populated, and boots to something broken — the
// operator has no reason to suspect it until a machine fails to install.
//
// Each platform asks its own power manager, and every one of them is
// advisory: nothing here can stop a lid closing or a battery running out, so
// the readback verify remains the thing that actually proves the media.
// Where no mechanism is available the request is a no-op rather than an
// error, because being unable to inhibit sleep is not a reason to refuse to
// write.
package awake

// Keep asks the OS to stay awake and returns the function that releases it.
// The release is safe to call more than once, so `defer release()` beside an
// early return is fine.
func Keep(reason string) (release func()) { return keep(reason) }
