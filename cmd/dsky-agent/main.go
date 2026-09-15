// Command dsky-agent provisions a freshly imaged machine at its first sign-in:
// drivers, consumer-app removal and the operator's programs, from a manifest
// DSKY stages beside it. It replaces the scripts DSKY used to generate per
// build, which failed on the machine in ways that could not be tested before
// they were written.
package main

import (
	"fmt"
	"os"

	"github.com/uplinkresearch/dsky/internal/agent"
)

func main() {
	if err := agent.Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dsky-agent:", err)
		os.Exit(1)
	}
}
