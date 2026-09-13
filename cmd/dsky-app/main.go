// Command dsky-app is the windowless launcher for the local portal: it
// starts the server and opens the page in the default browser with no
// console window. On Windows it is linked for the GUI subsystem
// (-ldflags -H=windowsgui) so nothing flashes when the Start-menu icon is
// clicked; the portal's Quit button (or a signal) stops it. Errors go to a
// log file since there is no console to show them.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/uplinkresearch/dsky/internal/appconfig"
	"github.com/uplinkresearch/dsky/internal/cli"
)

func main() {
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
