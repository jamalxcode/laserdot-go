// laserdot is the command-line front end over the same core and index as
// the app (spec §13.4). Commands go through laserdotd when it runs.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/ipc"
	"github.com/jamalxcode/laserdot-go/internal/query"
	"github.com/jamalxcode/laserdot-go/internal/sandbox"
	"github.com/jamalxcode/laserdot-go/internal/watch"
)

const usage = `laserdot — private, media-aware file search

usage: laserdot [--db PATH] [--local] COMMAND [ARGS]

commands:
  index [FOLDER...]   index folders; with none, refresh everything indexed
  search QUERY        search — see 'laserdot help-syntax'
      --sort relevance|name|size|modified|created  --reverse
      --limit N  --offset N  --paths (-0)  --json  --no-typo
  watch [--poll N]    keep the index current as files change (no daemon needed)
  forget FOLDER       stop indexing a folder
  drives [ACTION]     removable drives: plan, index, unlock, detach, rebuild, delete, eject
  dupes [QUERY]       find duplicates (a dry run; --trash or --move-to DIR to clean up)
      --exact-only --no-images --no-videos --min-size 1mb --image-distance N --yes --json
  content [ACTION]    content search folders: status, add, remove, max-size, update
  deep [--limit N]    Tier 2: read legacy containers, HDR10+ and VFR with ffprobe (needs ffprobe)
  loudness FILE       EBU R128 loudness of a file (needs ffmpeg)
  roots               indexed folders
  stats               index size and indexed folders
  status              is laserdotd running, and is its network access denied?
  help-syntax [ar]    the query cheat sheet (English or Arabic)

--db     index database (default: $LASERDOT_DB or the per-user location)
--local  open the index directly even if laserdotd is running
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "laserdot:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	global := flag.NewFlagSet("laserdot", flag.ContinueOnError)
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	db := global.String("db", core.DefaultDBPath(), "")
	local := global.Bool("local", false, "")
	if err := global.Parse(args); err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := rest[0], rest[1:]

	switch cmd {
	case "help-syntax":
		lang := os.Getenv("LANG")
		if len(rest) > 0 {
			lang = rest[0]
		}
		fmt.Print(query.CheatSheetIn(query.LangFromCode(lang)))
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	case "status":
		return status(*db)
	}

	// No network for the CLI either (§7.1). It talks to the daemon's
	// socket, so that folder stays reachable.
	if _, err := sandbox.DenyNetwork(filepath.Dir(ipc.SocketFor(*db))); err != nil {
		return err
	}
	if cmd == "watch" {
		return watchCmd(*db, rest)
	}
	t, err := open(*db, *local)
	if err != nil {
		return err
	}
	defer t.close()

	switch cmd {
	case "index":
		if len(rest) == 0 {
			u, err := t.refresh()
			if err != nil {
				return err
			}
			fmt.Printf("refreshed: +%d −%d in %d ms\n", u.Added, u.Removed, u.MS)
			for _, r := range u.OfflineRoots {
				fmt.Printf("offline: %s\n", r)
			}
			return nil
		}
		for _, p := range rest {
			st, err := t.index(p)
			if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			if st.Incremental {
				fmt.Printf("%s: refreshed (+%d −%d)\n", st.Root, st.Added, st.Removed)
			} else {
				fmt.Printf("%s: %d files, %d folders (scan %d ms, write %d ms", st.Root, st.Files, st.Folders, st.ScanMS, st.WriteMS)
				if st.ScanErrors > 0 {
					fmt.Printf(", %d unreadable", st.ScanErrors)
				}
				fmt.Println(")")
			}
		}
		return nil
	case "search":
		return search(t, rest)
	case "drives":
		return drivesCmd(t, rest)
	case "dupes":
		return dupesCmd(t, rest)
	case "content":
		return contentCmd(t, rest)
	case "deep":
		limit := 0
		if len(rest) == 2 && rest[0] == "--limit" {
			limit, _ = strconv.Atoi(rest[1])
		}
		st, err := t.deep(limit)
		if err != nil {
			return err
		}
		if st.Tool == "" {
			return errors.New("ffprobe not found (set LASERDOT_FFPROBE or install ffmpeg)")
		}
		fmt.Printf("%d files read, %d updated, %d failed, %d left (%d ms) — %s\n", st.Probed, st.Updated, st.Failed, st.Remaining, st.MS, st.Tool)
		return nil
	case "loudness":
		if len(rest) != 1 {
			return errors.New("loudness needs a file")
		}
		l, err := t.loudness(rest[0])
		if err != nil {
			return err
		}
		fmt.Printf("integrated %.1f LUFS, true peak %.1f dBTP, range %.1f LU\n", l.Integrated, l.TruePeak, l.Range)
		return nil
	case "forget":
		if len(rest) != 1 {
			return errors.New("forget needs one folder")
		}
		ok, err := t.forget(rest[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s was not indexed", rest[0])
		}
		fmt.Println("forgot", rest[0])
		return nil
	case "roots":
		roots, err := t.roots()
		if err != nil {
			return err
		}
		for _, r := range roots {
			state := ""
			if r.Offline {
				state += " (offline)"
			}
			if !r.Complete {
				state += " (incomplete)"
			}
			fmt.Printf("%s%s\n", r.Path, state)
		}
		return nil
	case "stats":
		s, err := t.stats()
		if err != nil {
			return err
		}
		fmt.Printf("%d entries, %.1f MiB in memory, loaded in %d ms\n", s.Entries, float64(s.MemoryBytes)/(1<<20), s.LoadMS)
		for _, r := range s.Roots {
			fmt.Println("  " + r)
		}
		return nil
	}
	return fmt.Errorf("unknown command `%s` — see 'laserdot help'", cmd)
}

func status(db string) error {
	c := ipc.ConnectLive(ipc.SocketFor(db))
	if c == nil {
		fmt.Println("laserdotd is not running (socket", ipc.SocketFor(db)+")")
		return nil
	}
	defer c.Close()
	var st ipc.Status
	if err := c.Call(&ipc.Request{Op: ipc.OpStatus}, &st); err != nil {
		return err
	}
	denied := "DENIED by the OS"
	if !st.NetworkDenied {
		denied = "NOT denied — run laserdotd without --no-sandbox"
	}
	fmt.Printf("laserdotd %s (pid %d, protocol %d)\nnetwork: %s\nindex: %s (%d entries)\n",
		st.Version, st.PID, st.Protocol, denied, st.DB, st.Entries)
	return nil
}

func search(t target, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	sortName := fs.String("sort", "relevance", "")
	reverse := fs.Bool("reverse", false, "")
	limit := fs.Int("limit", 100, "")
	offset := fs.Int("offset", 0, "")
	paths := fs.Bool("paths", false, "")
	fs.BoolVar(paths, "0", false, "")
	asJSON := fs.Bool("json", false, "")
	noTypo := fs.Bool("no-typo", false, "")
	// Flags may follow the query words: pull them out first.
	var words, flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") || a == "-0" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if !strings.Contains(name, "=") && (name == "sort" || name == "limit" || name == "offset") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		words = append(words, a)
	}
	if err := fs.Parse(flags); err != nil {
		return err
	}
	srt, ok := index.ParseSort(*sortName)
	if !ok {
		return fmt.Errorf("unknown sort `%s`", *sortName)
	}
	q := strings.Join(words, " ")
	res, err := t.search(q, index.Page{Sort: srt, Reverse: *reverse, Offset: *offset, Limit: *limit}, !*noTypo)
	if err != nil {
		if qe := queryError(q, err); qe != err {
			if cs, cerr := t.coach(q); cerr == nil {
				for _, c := range cs {
					if c.Kind == core.CoachIndexFolder {
						return fmt.Errorf("%v — index it: laserdot index %s", qe, c.Path)
					}
				}
			}
			return qe
		}
		return err
	}
	for _, w := range res.Warnings {
		s := ""
		if w.Suggestion != "" {
			s = " — did you mean `" + w.Suggestion + "`?"
		}
		fmt.Fprintf(os.Stderr, "note: %s%s\n", w.Message, s)
	}
	if res.Total == 0 && !*asJSON && !*paths {
		if cs, err := t.coach(q); err == nil {
			fmt.Fprintln(os.Stderr, "nothing found. try:")
			printCoaching(cs)
		}
	}
	switch {
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	case *paths:
		for _, h := range res.Hits {
			fmt.Println(h.Path)
		}
		return nil
	}
	for _, h := range res.Hits {
		extra := ""
		if h.Width != nil && h.Height != nil {
			extra = fmt.Sprintf("  %d×%d", *h.Width, *h.Height)
		}
		if m := h.Media; m != nil {
			var parts []string
			if m.VideoCodec != nil {
				parts = append(parts, strings.Join(append([]string{*m.VideoCodec}, m.HDR...), " "))
			}
			if len(m.Audio) > 0 {
				a := m.Audio[0]
				if len(m.Audio) > 1 {
					a += fmt.Sprintf(" +%d", len(m.Audio)-1)
				}
				parts = append(parts, a)
			}
			if m.DurationMS != nil && *m.DurationMS >= 1000 {
				s := *m.DurationMS / 1000
				parts = append(parts, fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60))
			}
			if len(parts) > 0 {
				extra += "  " + strings.Join(parts, " · ")
			}
		}
		if h.Volume != nil {
			extra += "  [" + *h.Volume + "]"
		}
		if h.Content {
			extra += "  (contents)"
		}
		if h.Typo {
			extra += "  (typo match)"
		}
		size := humanSize(h.Size)
		if h.IsDir {
			size = "folder"
		}
		fmt.Printf("%-10s %s  %s%s\n", size, time.Unix(h.Modified, 0).Format("2006-01-02"), h.Path, extra)
	}
	shown := ""
	if uint64(len(res.Hits)) < res.Total {
		shown = fmt.Sprintf(" (showing %d)", len(res.Hits))
	}
	fmt.Fprintf(os.Stderr, "%d results%s in %.1f ms\n", res.Total, shown, float64(res.ElapsedUS)/1000)
	return nil
}

// queryError underlines the error's position in the query (§8.1).
func queryError(q string, err error) error {
	var start, end int
	var msg, sugg string
	if qe, ok := core.IsQueryError(err); ok {
		start, end, msg, sugg = qe.Span.Start, qe.Span.End, qe.Message, qe.Suggestion
	} else if re, ok := err.(*ipc.RemoteError); ok && re.Span != nil {
		start, end, msg = re.Span.Start, re.Span.End, re.Message
		if re.Suggestion != nil {
			sugg = *re.Suggestion
		}
	} else {
		return err
	}
	fmt.Fprintln(os.Stderr, q)
	pad := []rune(q)
	if start > len(pad) {
		start = len(pad)
	}
	fmt.Fprintf(os.Stderr, "%s%s\n", strings.Repeat(" ", start), strings.Repeat("^", max(end-start, 1)))
	if sugg != "" {
		msg += " — try `" + sugg + "`"
	}
	return errors.New(msg)
}

func humanSize(b uint64) string {
	const unit = 1000 // decimal, as Finder
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	f, i := float64(b), 0
	for f >= unit && i < 4 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", f, [...]string{"B", "kB", "MB", "GB", "TB"}[i])
}

func watchCmd(db string, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	poll := fs.Int("poll", 0, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if c := ipc.ConnectLive(ipc.SocketFor(db)); c != nil {
		c.Close()
		return errors.New("laserdotd is running and already keeps the index current")
	}
	ld, err := core.Open(db)
	if err != nil {
		return err
	}
	defer ld.Close()
	opts := watch.DefaultOptions()
	if *poll > 0 {
		opts.ForcePoll, opts.PollInterval = true, time.Duration(*poll)*time.Second
	}
	w := ld.Watch(opts, core.WatchEvents{
		Started: func(b watch.Backend) { fmt.Fprintln(os.Stderr, "watching with", b, "— Ctrl-C to stop") },
		Error:   func(err error) { fmt.Fprintln(os.Stderr, "update failed:", err) },
		Applied: func(u core.UpdateStats) {
			fmt.Printf("+%d −%d ~%d renamed %d (%d ms)\n", u.Added, u.Removed, u.Modified, u.Renamed, u.MS)
		},
	})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	w.Stop()
	return nil
}
