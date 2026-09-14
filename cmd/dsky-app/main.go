// Command dsky-app is the windowless launcher for the local portal: it
// starts the server and opens the page in the default browser with no
// console window. On Windows it is linked for the GUI subsystem
// (-ldflags -H=windowsgui) so nothing flashes when the Start-menu icon is
// clicked; the portal's Quit button (or a signal) stops it. Errors go to a
// log file since there is no console to show them.
//
// The icon is a Windows resource in rsrc_windows_*.syso, which the Go linker
// picks up by filename for Windows builds only. Regenerate both commands'
// copies after changing dsky.ico:
//
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --in ../../winres/winres.json --out rsrc --arch amd64,arm64
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --in ../../winres/winres.json --out ../dsky/rsrc --arch amd64,arm64
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/uplinkresearch/dsky/internal/appconfig"
	"github.com/uplinkresearch/dsky/internal/cli"
)

func main() {
	// Writing and erasing sticks on Windows relaunches this program as
	// administrator with "flash-worker". It used to ignore that, find the app
	// already running, raise its window and exit 0, so every erase or write
	// started from the app reported success having done nothing.
	if len(os.Args) > 1 && os.Args[1] == "flash-worker" {
		os.Exit(cli.Main(os.Args[1:]))
	}
	dir := appconfig.Dir()
	_ = os.MkdirAll(dir, 0o755)
	if f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f)
		defer f.Close()
	}
	if err := cli.AppMain(); err != nil {
		log.Printf("dsky-app: %v", err)
		os.Exit(1)
	}
}
