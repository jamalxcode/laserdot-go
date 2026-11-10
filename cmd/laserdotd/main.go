// laserdotd is the index daemon (spec §13.2a).
//
//   - Denies itself network access before anything else (§7.1).
//   - Is the single writer of the index; the app and the CLI are clients.
//   - Serves JSON-line requests on a Unix socket (owner-only permissions).
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/daemon"
	"github.com/jamalxcode/laserdot-go/internal/ipc"
	"github.com/jamalxcode/laserdot-go/internal/sandbox"
	"github.com/jamalxcode/laserdot-go/internal/watch"
)

const usage = "laserdotd [--db PATH] [--socket PATH] [--poll SECONDS] [--no-watch] [--no-sandbox]"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "laserdotd:", err)
		os.Exit(1)
	}
}

func run() error {
	db, socket, noSandbox, noWatch := core.DefaultDBPath(), "", false, false
	wopts := watch.DefaultOptions()
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", args[i])
			}
			i++
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--db":
			db, err = next()
		case "--socket":
			socket, err = next()
		case "--poll": // poll every N seconds instead of using file events
			var v string
			if v, err = next(); err == nil {
				var n int
				if n, err = strconv.Atoi(v); err == nil && n > 0 {
					wopts.ForcePoll, wopts.PollInterval = true, time.Duration(n)*time.Second
				} else {
					err = fmt.Errorf("--poll needs seconds")
				}
			}
		case "--no-watch":
			noWatch = true
		case "--no-sandbox": // for debugging only; the app never passes it
			noSandbox = true
		case "--version":
			fmt.Println("laserdotd", daemon.Version)
			return nil
		case "-h", "--help":
			fmt.Println(usage)
			return nil
		default:
			return fmt.Errorf("unknown argument `%s`\n%s", args[i], usage)
		}
		if err != nil {
			return err
		}
	}
	if socket == "" {
		socket = ipc.SocketFor(db)
	}
	dir := filepath.Dir(socket)
	for _, d := range []string{dir, filepath.Dir(db)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}

	// 1. No network, before anything else runs (§7.1). On macOS this
	// re-executes laserdotd under the sandbox profile.
	if noSandbox {
		fmt.Fprintln(os.Stderr, "laserdotd: WARNING running without the network sandbox (--no-sandbox)")
	} else {
		e, err := sandbox.DenyNetwork(dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "laserdotd: network access denied by the OS (%s)\n", e)
	}

	// 2. One daemon per socket.
	if c := ipc.ConnectLive(socket); c != nil {
		c.Close()
		fmt.Fprintln(os.Stderr, "laserdotd: already running on", socket)
		return nil
	}

	// 3. The index.
	ld, err := core.Open(db)
	if err != nil {
		return err
	}
	defer ld.Close()
	st := ld.Stats()
	fmt.Fprintf(os.Stderr, "laserdotd: %d entries loaded in %d ms\n", st.Entries, st.LoadMS)

	// 4. Serve.
	srv, err := daemon.Listen(ld, socket)
	if err != nil {
		return err
	}
	if !noWatch {
		w := ld.Watch(wopts, core.WatchEvents{
			Started: func(b watch.Backend) { fmt.Fprintln(os.Stderr, "laserdotd: watching with", b) },
			Error:   func(err error) { fmt.Fprintln(os.Stderr, "laserdotd: update failed:", err) },
			Applied: func(u core.UpdateStats) {
				fmt.Fprintf(os.Stderr, "laserdotd: +%d −%d ~%d renamed %d (%d ms)\n", u.Added, u.Removed, u.Modified, u.Renamed, u.MS)
			},
		})
		defer w.Stop()
		srv.SetWatcher(w)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			srv.Stop()
		case <-srv.Done():
		}
	}()
	fmt.Fprintln(os.Stderr, "laserdotd: listening on", socket)
	return srv.Serve()
}
