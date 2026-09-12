package cli

import (
	"context"

	"github.com/DustanBaker/uplink-composer/internal/tui"
)

// cmdTUI runs the full-screen wizard — the same Quick Install pipeline as
// `uplink install` and the web portal, without having to know the flags.
func cmdTUI(ctx context.Context, env *Env, _ []string) error {
	lib, err := env.library()
	if err != nil {
		return err
	}
	return tui.Run(ctx, lib)
}
