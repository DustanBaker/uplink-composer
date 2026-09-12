package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/DustanBaker/the-composer/internal/jobs"
	"github.com/DustanBaker/the-composer/internal/webui"
)

func cmdServe(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 8931, "loopback port")
	open := fs.Bool("open", false, "open the page in the default browser")
	if err := parseFlags(fs, args); err != nil {
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
	url := fmt.Sprintf("http://%s/#t=%s", addr, s.Token)
	fmt.Printf("The Uplink CompOSer — workspace %q (%s)\n", ws.Config.Org.Name, ws.Dir)
	fmt.Printf("Open:  %s\n", url)
	fmt.Println("The token in the URL is this session's key — the page needs it. Ctrl-C stops the server.")
	if *open {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(url)
		}()
	}
	return webui.Serve(ctx, addr, s)
}

// openBrowser launches the platform's default browser; failures are silent
// (the URL is printed anyway).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
