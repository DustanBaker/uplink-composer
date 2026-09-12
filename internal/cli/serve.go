package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/DustanBaker/uplink-composer/internal/appconfig"
	"github.com/DustanBaker/uplink-composer/internal/jobs"
	"github.com/DustanBaker/uplink-composer/internal/library"
	"github.com/DustanBaker/uplink-composer/internal/webui"
)

func cmdServe(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 8931, "loopback port (auto-increments if busy)")
	open := fs.Bool("open", false, "open the page in the default browser")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	url, done, err := startServer(ctx, lib, env.Vars, *port, env.WorkspaceDir, *open, true)
	if err != nil {
		return err
	}
	fmt.Printf("Open:  %s\n", url)
	fmt.Println("The token in the URL is this session's key — the page needs it. Ctrl-C (or Quit in the page) stops it.")
	<-done
	return nil
}

// AppMain is the entry point for the windowless launcher (uplink-app): open
// the portal in the browser and serve until the page's Quit button or a
// signal stops it.
func AppMain() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	lib, err := library.Open(library.DefaultRoot())
	if err != nil {
		return err
	}
	_, done, err := startServer(ctx, lib, map[string]string{}, 8931, ".", true, false)
	if err != nil {
		return err
	}
	<-done
	return nil
}

// startServer binds a free port at or after base, wires the workspace
// (explicit -w if present, else the last-opened one), optionally opens the
// browser, and runs the server in the background. It returns the tokened URL
// and a channel that closes when the server stops.
func startServer(ctx context.Context, lib *library.Library, vars map[string]string, base int, wsDir string, open, announce bool) (string, <-chan struct{}, error) {
	var tok [16]byte
	if _, err := rand.Read(tok[:]); err != nil {
		return "", nil, err
	}
	cfg := appconfig.Load()
	s := &webui.Server{
		Lib: lib, CLIVars: vars, Token: hex.EncodeToString(tok[:]),
		Reg: jobs.NewRegistry(), Cfg: cfg,
	}
	if err := s.SetWorkspaceDir(wsDir); err != nil {
		if last := cfg.Current(); last != "" {
			_ = s.SetWorkspaceDir(last)
		}
	}

	ln, port, err := listen(base)
	if err != nil {
		return "", nil, err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/#t=%s", port, s.Token)
	if announce {
		if name := s.WorkspaceName(); name != "" {
			fmt.Printf("The Uplink CompOSer — workspace %q\n", name)
		} else {
			fmt.Println("The Uplink CompOSer — no workspace open yet (pick one in the page)")
		}
	}
	done := make(chan struct{})
	go func() { _ = webui.Serve(ctx, ln, s); close(done) }()
	if open {
		go func() { time.Sleep(300 * time.Millisecond); openBrowser(url) }()
	}
	return url, done, nil
}

// listen binds 127.0.0.1 at base, then base+1..base+19 if busy.
func listen(base int) (net.Listener, int, error) {
	for p := base; p < base+20; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, p, nil
		}
	}
	return nil, 0, fmt.Errorf("no free loopback port in %d..%d", base, base+20)
}

// openBrowser launches the platform's default browser; failures are silent.
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
