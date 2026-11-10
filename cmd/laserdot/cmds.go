package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/index"
)

// readPassphrase reads one line from stdin (--passphrase-stdin), so it
// never appears in the process list or shell history.
func readPassphrase(use bool) (string, error) {
	if !use {
		return "", nil
	}
	fmt.Fprint(os.Stderr, "passphrase: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

const drivesUsage = `laserdot drives [ACTION MOUNT]

  (no action)        list mounted drives and their index state
  plan MOUNT         estimate a drive's index without writing anything
  index MOUNT        create a drive's portable index [--passphrase-stdin to encrypt]
  unlock MOUNT       attach an existing index [--passphrase-stdin for an encrypted one]
  detach MOUNT       stop using a drive's index
  rebuild MOUNT      delete and recreate the index [--passphrase-stdin]
  delete MOUNT       delete the drive's index
  eject MOUNT        detach and eject (macOS)
`

func drivesCmd(t target, args []string) error {
	fs := flag.NewFlagSet("drives", flag.ContinueOnError)
	usePass := fs.Bool("passphrase-stdin", false, "")
	asJSON := fs.Bool("json", false, "")
	var pos []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			if err := fs.Parse([]string{a}); err != nil {
				return err
			}
		} else {
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 {
		ds, err := t.drives()
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(ds)
		}
		if len(ds) == 0 {
			fmt.Println("no drives mounted")
		}
		for _, d := range ds {
			extra := ""
			if d.State.State == core.DriveAttached {
				extra = fmt.Sprintf(", %d entries", d.Entries)
				if !d.Complete {
					extra += ", incomplete"
				}
			}
			if d.Encrypted {
				extra += ", encrypted"
			}
			fmt.Printf("%-20s %-10s %s (%s%s)\n", d.Name, d.State.State, d.Mount, d.FSType, extra)
		}
		return nil
	}
	if len(pos) != 2 {
		fmt.Fprint(os.Stderr, drivesUsage)
		return errors.New("which drive?")
	}
	action, mount := pos[0], pos[1]
	pass, err := readPassphrase(*usePass)
	if err != nil {
		return err
	}
	switch action {
	case "plan":
		p, err := t.drivePlan(mount)
		if err != nil {
			return err
		}
		where := "on the drive"
		if !p.OnDrive {
			where = "on this Mac (the drive is read-only)"
		}
		fmt.Printf("%s: %d files, %d folders, %s\nthe index will take about %s, %s:\n  %s\n",
			p.Name, p.Files, p.Folders, humanSize(p.Bytes), humanSize(p.IndexEstimateBytes), where, p.Location)
	case "index", "rebuild":
		f := t.driveIndex
		if action == "rebuild" {
			f = t.driveRebuild
		}
		st, err := f(mount, pass)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d files, %d folders indexed (%d media read)\n", mount, st.Files, st.Folders, st.Probed)
	case "unlock", "attach":
		d, err := t.driveAttach(mount, pass)
		if err != nil {
			return err
		}
		fmt.Printf("%s attached (%d entries)\n", d.Name, d.Entries)
	case "detach":
		ok, err := t.driveDetach(mount)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s was not attached", mount)
		}
		fmt.Println("detached", mount)
	case "delete":
		ok, err := t.driveDelete(mount)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s has no index", mount)
		}
		fmt.Println("deleted the index of", mount)
	case "eject":
		if err := t.driveEject(mount); err != nil {
			return err
		}
		fmt.Println("ejected", mount)
	default:
		fmt.Fprint(os.Stderr, drivesUsage)
		return fmt.Errorf("unknown action %q", action)
	}
	return nil
}

// parseSize reads 100kb, 5mb, 1gb or bytes (decimal, as Finder).
func parseSize(s string) (uint64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := uint64(1)
	for _, u := range []struct {
		suffix string
		m      uint64
	}{{"tb", 1e12}, {"gb", 1e9}, {"mb", 1e6}, {"kb", 1e3}, {"b", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.m
			break
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("sizes look like 100kb, 5mb or 1gb")
	}
	return uint64(f * float64(mult)), nil
}

func dupesCmd(t target, args []string) error {
	fs := flag.NewFlagSet("dupes", flag.ContinueOnError)
	exactOnly := fs.Bool("exact-only", false, "")
	noImages := fs.Bool("no-images", false, "")
	noVideos := fs.Bool("no-videos", false, "")
	minSize := fs.String("min-size", "1", "")
	dist := fs.Int("image-distance", 7, "")
	trash := fs.Bool("trash", false, "")
	moveTo := fs.String("move-to", "", "")
	yes := fs.Bool("yes", false, "")
	asJSON := fs.Bool("json", false, "")
	var words, flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			flags = append(flags, a)
			if n := strings.TrimLeft(a, "-"); !strings.Contains(n, "=") && (n == "min-size" || n == "image-distance" || n == "move-to") && i+1 < len(args) {
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
	if *trash && *moveTo != "" {
		return errors.New("--trash and --move-to: pick one")
	}
	o := core.DefaultDupeOptions()
	o.Scope = strings.Join(words, " ")
	o.Images, o.Videos = !*noImages && !*exactOnly, !*noVideos && !*exactOnly
	o.ImageDistance = *dist
	var err error
	if o.MinSize, err = parseSize(*minSize); err != nil {
		return err
	}
	rep, err := t.dupes(o)
	if err != nil {
		return err
	}
	if *asJSON && !*trash && *moveTo == "" {
		return printJSON(rep)
	}
	for _, g := range rep.Groups {
		fmt.Printf("%s — %s reclaimable\n", strings.ReplaceAll(g.Kind, "_", " "), humanSize(g.Reclaimable))
		for _, m := range g.Members {
			mark := "  remove"
			if m.Keep {
				mark = "  KEEP  "
			}
			vol := ""
			if m.Volume != nil {
				vol = " [" + *m.Volume + "]"
			}
			fmt.Printf("%s %s%s  %s %s\n", mark, m.Path, vol, humanSize(m.Size), m.Summary)
		}
	}
	fmt.Fprintf(os.Stderr, "%d groups, %s reclaimable; %d files checked, %d read (%s) in %d ms\n",
		len(rep.Groups), humanSize(rep.Reclaimable), rep.Files, rep.Read, humanSize(rep.BytesRead), rep.MS)
	if o.Videos && rep.VideoDecoder == nil {
		fmt.Fprintln(os.Stderr, "note: no video decoder (ffmpeg) found: re-encoded videos were not compared")
	}
	if !*trash && *moveTo == "" || len(rep.Groups) == 0 {
		return nil
	}
	dest := core.CleanupDest{To: "trash"}
	if *moveTo != "" {
		dest = core.CleanupDest{To: "folder", Folder: *moveTo}
	}
	req := rep.Recommended(dest)
	plan, err := t.cleanupPlan(req)
	if err != nil {
		return err
	}
	for _, s := range plan.Skipped {
		fmt.Fprintln(os.Stderr, "skipped:", s)
	}
	if len(plan.Moves) == 0 {
		return errors.New("nothing to move")
	}
	if !*yes {
		where := "the Trash"
		if *moveTo != "" {
			where = *moveTo
		}
		fmt.Fprintf(os.Stderr, "move %d files (%s) to %s? [y/N] ", len(plan.Moves), humanSize(plan.Bytes), where)
		ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			return errors.New("nothing moved")
		}
	}
	res, err := t.cleanup(req)
	if err != nil {
		return err
	}
	for _, f := range res.Failed {
		fmt.Fprintln(os.Stderr, "failed:", f)
	}
	fmt.Printf("moved %d files, %s freed here\n", len(res.Moved), humanSize(res.Bytes))
	return nil
}

const contentUsage = `laserdot content [status | add FOLDER... | remove FOLDER... | max-size SIZE | update]
`

func contentCmd(t target, args []string) error {
	action := "status"
	if len(args) > 0 {
		action, args = args[0], args[1:]
	}
	st, err := t.contentStatus()
	if err != nil {
		return err
	}
	s := core.ContentSettings{MaxBytes: index.DefaultContentMaxBytes}
	if st.Settings != nil {
		s = *st.Settings
	}
	switch action {
	case "status":
		fmt.Printf("%d documents indexed, %d skipped, %d waiting\nfolders (files up to %s):\n", st.Documents, st.Skipped, st.Pending, humanSize(s.MaxBytes))
		for _, r := range s.Roots {
			fmt.Println("  " + r)
		}
		return nil
	case "add", "remove":
		if len(args) == 0 {
			return errors.New("which folders?")
		}
		for _, a := range args {
			if action == "add" {
				s.Roots = append(s.Roots, a)
			} else if i := indexOf(s.Roots, a); i >= 0 {
				s.Roots = append(s.Roots[:i], s.Roots[i+1:]...)
			} else {
				return fmt.Errorf("%s is not a content folder", a)
			}
		}
	case "max-size":
		if len(args) != 1 {
			return errors.New("max-size needs a size, e.g. 20mb")
		}
		if s.MaxBytes, err = parseSize(args[0]); err != nil {
			return err
		}
	case "update":
		u, err := t.updateContent()
		if err != nil {
			return err
		}
		fmt.Printf("%d indexed, %d skipped, %d removed (%s read) in %d ms\n", u.Indexed, u.Skipped, u.Removed, humanSize(u.BytesRead), u.MS)
		return nil
	default:
		fmt.Fprint(os.Stderr, contentUsage)
		return fmt.Errorf("unknown action %q", action)
	}
	if _, err := t.setContent(s); err != nil {
		return err
	}
	return contentCmd(t, nil)
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s || strings.TrimSuffix(v, "/") == strings.TrimSuffix(s, "/") {
			return i
		}
	}
	return -1
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printCoaching suggests what to try when nothing matched (§8.3).
func printCoaching(cs []core.Coaching) {
	for _, c := range cs {
		switch c.Kind {
		case core.CoachIndexFolder:
			fmt.Fprintf(os.Stderr, "  %s is not indexed — laserdot index %s\n", c.Path, c.Path)
		case core.CoachFixValue:
			fmt.Fprintf(os.Stderr, "  did you mean %q? %d results: laserdot search '%s'\n", c.To, c.Count, c.Query)
		case core.CoachRelax:
			fmt.Fprintf(os.Stderr, "  without %s: %d results: laserdot search '%s'\n", c.Removed, c.Count, c.Query)
		case core.CoachShowSyntax:
			fmt.Fprintln(os.Stderr, "  the query syntax: laserdot help-syntax")
		}
	}
}
