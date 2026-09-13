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

	"github.com/uplinkresearch/bootwright/internal/appconfig"
	"github.com/uplinkresearch/bootwright/internal/jobs"
	"github.com/uplinkresearch/bootwright/internal/library"
	"github.com/uplinkresearch/bootwright/internal/webui"
)

// idleGrace is how long the portal waits after the last page closes. Long
// enough that a reload (which drops the event stream and reopens it) is not
// mistaken for leaving, short enough that a forgotten tab does not leave a
// server running all afternoon.
const idleGrace = 45 * time.Second

func cmdServe(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 8931, "loopback port (auto-increments if busy)")
	open := fs.Bool("open", false, "open the page in the default browser")
	stay := fs.Bool("keep-alive", false, "keep serving after the last page closes")
	newInst := fs.Bool("new", false, "start another portal even if one is running")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	lib, err := env.library()
	if err != nil {
		return err
	}
	if !*newInst {
		if inst, ok := liveInstance(lib.Root); ok {
			fmt.Printf("A portal is already running.\n\nOpen:  %s\n", inst.url())
			if *open {
				openBrowser(inst.url())
			}
			fmt.Println("\n(`bootwright serve --new` starts a second one anyway.)")
			return nil
		}
	}
	idle := idleGrace
	if *stay {
		idle = 0
	}
	url, done, err := startServer(ctx, lib, env.Vars, *port, env.WorkspaceDir, *open, true, idle)
	if err != nil {
		return err
	}
	fmt.Printf("Open:  %s\n", url)
	fmt.Println("The token in the URL is this session's key — the page needs it.")
	if *stay {
		fmt.Println("Ctrl-C (or Quit in the page) stops it.")
	} else {
		// Said plainly, because a server that stops on its own is surprising
		// if you were not told — and this is the behaviour people expect from
		// something they closed.
		fmt.Println("It stops on its own shortly after you close the page. Anything still")
		fmt.Println("running — a flash, a build — keeps it open until it finishes.")
	}
	<-done
	return nil
}

// AppMain is the entry point for the windowless launcher (bootwright-app): open
// the portal in the browser and serve until the page's Quit button or a
// signal stops it.
func AppMain() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	lib, err := library.Open(library.DefaultRoot())
	if err != nil {
		return err
	}
	// Launching the app when it is already running should bring back the
	// portal you already have, not start a second one behind it.
	if inst, ok := liveInstance(lib.Root); ok {
		// Its own window, raised. Failing that the portal is running without
		// one — a `serve` in a terminal — so give it one, or a browser.
		if focusWindow(appTitle) {
			return nil
		}
		if !showWindow(inst.url(), appTitle) {
			openBrowser(inst.url())
		}
		return nil
	}
	// open=false: the window below is the way in. Only the browser fallback
	// needs one opened for it.
	url, done, err := startServer(ctx, lib, map[string]string{}, 8931, ".", false, false, idleGrace)
	if err != nil {
		return err
	}
	if showWindow(url, appTitle) {
		// The window is the app. Closing it quits, which is the gesture
		// everybody already uses and previously did nothing.
		//
		// A flash in progress is not lost: writes run in a separate elevated
		// worker, so the stick is finished and verified even though the
		// portal that started it has gone.
		stop()
		// Give the server its moment to unwind and delete the record of itself.
		// Returning straight after stop() races that, and losing the race
		// strands a record pointing at a dead port — recoverable, since the
		// next launch probes rather than trusts it, but it costs that launch a
		// timeout for no reason. Bounded, so a wedged server cannot leave the
		// process hanging around invisibly after its window has gone.
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
		return nil
	}
	// No window to be had — fall back to the browser, where the idle timeout
	// is what eventually stops the server.
	openBrowser(url)
	<-done
	return nil
}

// appTitle is what the window is called in the taskbar and the title bar.
const appTitle = "Bootwright"

// startServer binds a free port at or after base, wires the workspace
// (explicit -w if present, else the last-opened one), optionally opens the
// browser, and runs the server in the background. It returns the tokened URL
// and a channel that closes when the server stops.
func startServer(ctx context.Context, lib *library.Library, vars map[string]string, base int, wsDir string, open, announce bool, idle time.Duration) (string, <-chan struct{}, error) {
	var tok [16]byte
	if _, err := rand.Read(tok[:]); err != nil {
		return "", nil, err
	}
	cfg := appconfig.Load()
	s := &webui.Server{
		Lib: lib, CLIVars: vars, Token: hex.EncodeToString(tok[:]),
		Reg: jobs.NewRegistry(), Cfg: cfg, IdleTimeout: idle,
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
			fmt.Printf("Bootwright — workspace %q\n", name)
		} else {
			fmt.Println("Bootwright — no workspace open yet (pick one in the page)")
		}
	}
	// Recorded before serving, so a second launch a moment later finds it.
	inst := instance{Port: port, Token: s.Token, PID: os.Getpid(),
		Started: time.Now().Format(time.RFC3339)}
	writeInstance(lib.Root, inst)

	done := make(chan struct{})
	go func() {
		_ = webui.Serve(ctx, ln, s)
		clearInstance(lib.Root, inst)
		close(done)
	}()
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
