// Command dsky builds bootable installation USB media from org
// workspaces: pull pinned OS images and driver packs, compose unattended
// install media, and write verified USB sticks — on Windows, macOS, and
// Linux.
package main

import (
	"os"

	"github.com/uplinkresearch/dsky/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
