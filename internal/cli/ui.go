package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/ui"
)

var uiCommand = &command{
	name:    "ui",
	summary: "Browse the recorded turns in a web page",
	usage: `Usage: turnback ui [--port <n>] [--no-open]

Open a web page for browsing the recorded turns: what each turn changed,
file by file, with its diff. Turns recorded while the page is open show up
on their own. The page only reads; to undo a turn it shows the command to
run here.

turnback serves the page from this computer only, at 127.0.0.1, behind an
address with a secret that changes every time. Press Ctrl-C to stop it.

Options:
      --port <n>  serve on this port instead of a free one
      --no-open   print the address without opening a browser
`,
	run: runUI,
}

// openURL and uiSignals are replaced in tests.
var (
	openURL   = openBrowser
	uiSignals = func() (<-chan os.Signal, func()) {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		return c, func() { signal.Stop(c) }
	}
)

func runUI(env *Env, args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	var port int
	var noOpen bool
	fs.IntVar(&port, "port", 0, "")
	fs.BoolVar(&noOpen, "no-open", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("ui takes no arguments").withHint("Run 'turnback help ui' for usage.")
	}
	if port < 0 || port > 65535 {
		return usageErrorf("--port must be a port number from 1 to 65535")
	}
	a, err := app.Open(env.Dir, env.Now)
	if err != nil {
		return explain(env, err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) {
			err = op.Err
		}
		return errorf("cannot serve on port %d: %v", port, err).
			withHint("Pick another port with --port, or leave --port out to use a free one.")
	}
	srv, err := ui.New(a, ln.Addr().String())
	if err != nil {
		ln.Close()
		return err
	}
	signals, stopSignals := uiSignals()
	defer stopSignals()
	web := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- web.Serve(ln) }()

	fmt.Fprintf(env.Stdout, "Serving the turns of %s at\n\n    %s\n\n", shownText(filepath.Base(a.Root)), srv.URL())
	if !noOpen {
		if err := openURL(srv.URL()); err != nil {
			fmt.Fprintf(env.Stderr, "turnback: could not open a browser (%v). Open the address above instead.\n", err)
		}
	}
	fmt.Fprintln(env.Stdout, "Press Ctrl-C to stop.")

	select {
	case <-signals:
	case err := <-served:
		return errorf("the web server stopped: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	web.Shutdown(ctx)
	fmt.Fprintln(env.Stdout, "Stopped.")
	return nil
}

// openBrowser opens url in the default browser without waiting for it.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no graphical session")
		}
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
