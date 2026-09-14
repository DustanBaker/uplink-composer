// Command mediaformats runs DSKY's own UDF reader and WIM splitter from the
// command line, so CI can check them against the real tools: Linux's UDF
// driver, wimlib-imagex and Microsoft's DISM.
//
//	mediaformats udf-extract <image> <dir>
//	mediaformats wim-split <src.wim> <first.swm> <max MiB>
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/uplinkresearch/dsky/internal/udf"
	"github.com/uplinkresearch/dsky/internal/wim"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: udf-extract <image> <dir> | wim-split <src.wim> <first.swm> <max MiB>")
	}
	switch args[0] {
	case "udf-extract":
		if len(args) != 3 {
			return fmt.Errorf("udf-extract <image> <dir>")
		}
		return udf.ExtractFile(args[1], args[2], nil)
	case "wim-split":
		if len(args) != 4 {
			return fmt.Errorf("wim-split <src.wim> <first.swm> <max MiB>")
		}
		mib, err := strconv.ParseInt(args[3], 10, 64)
		if err != nil {
			return err
		}
		parts, err := wim.Split(args[1], args[2], mib<<20, nil)
		if err != nil {
			return err
		}
		for _, p := range parts {
			fmt.Println(p)
		}
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}
