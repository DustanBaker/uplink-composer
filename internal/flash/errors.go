package flash

import "errors"

// ErrNeedsElevation signals that opening the raw device was denied and the
// caller should relaunch the flash in an elevated worker.
var ErrNeedsElevation = errors.New("flash: administrator/root rights required to open the raw device")
