package cli

import (
	"flag"
	"strings"
)

// parseFlags parses fs allowing flags anywhere on the line. The standard
// library stops at the first positional, which turned `init <dir> --org X`
// into a silent no-op; this reorders flags ahead of positionals first.
func parseFlags(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if eq := strings.Index(name, "="); eq >= 0 {
			continue // value attached
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // let fs.Parse report the unknown flag
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return fs.Parse(append(flags, positional...))
}
