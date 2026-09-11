package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"

	"github.com/DustanBaker/the-composer/internal/jobs"
	"github.com/DustanBaker/the-composer/internal/webui"
)

func cmdServe(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 8931, "loopback port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ws, err := env.workspace()
	if err != nil {
		return err
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	var tok [16]byte
	if _, err := rand.Read(tok[:]); err != nil {
		return err
	}
	s := &webui.Server{
		WS: ws, Lib: lib, CLIVars: env.Vars,
		Token: hex.EncodeToString(tok[:]),
		Reg:   jobs.NewRegistry(),
	}
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Printf("The Composer — workspace %q (%s)\n", ws.Config.Org.Name, ws.Dir)
	fmt.Printf("Open:  http://%s/#t=%s\n", addr, s.Token)
	fmt.Println("The token in the URL is this session's key — the page needs it. Ctrl-C stops the server.")
	return webui.Serve(ctx, addr, s)
}
