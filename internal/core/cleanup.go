package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// Duplicate cleanup (spec §4.4) never deletes: chosen copies move to the
// Trash or a folder, after a dry run, and a group is left alone unless
// every kept copy is unchanged since the scan.

// CleanupItem is a file as the scan saw it.
type CleanupItem struct {
	Path     string `json:"path"`
	Size     uint64 `json:"size"`
	Modified int64  `json:"modified"`
}

// CleanupGroup: copies to keep and copies to move away.
type CleanupGroup struct {
	Keep   []CleanupItem `json:"keep"`
	Remove []CleanupItem `json:"remove"`
}

// CleanupDest is where removed copies go: the Trash, or a folder.
// JSON: {"to":"trash"} or {"to":"folder","folder":"/path"}.
type CleanupDest struct {
	To     string `json:"to"` // "trash" or "folder"
	Folder string `json:"folder,omitempty"`
}

// CleanupRequest is a cleanup to plan or apply.
type CleanupRequest struct {
	Groups []CleanupGroup `json:"groups"`
	Dest   CleanupDest    `json:"dest"`
}

// PlannedMove is one move; To is empty for the Trash.
type PlannedMove struct {
	From string  `json:"from"`
	To   *string `json:"to"`
	Size uint64  `json:"size"`
}

// CleanupPlan is a dry run.
type CleanupPlan struct {
	Moves   []PlannedMove `json:"moves"`
	Bytes   uint64        `json:"bytes"`
	Skipped []string      `json:"skipped"`
}

// CleanupResult reports an applied cleanup.
type CleanupResult struct {
	Moved   []PlannedMove `json:"moved"`
	Bytes   uint64        `json:"bytes"`
	Skipped []string      `json:"skipped"`
	Failed  []string      `json:"failed"`
}

// Recommended keeps each group's best copy and moves the rest.
func (r *DupeReport) Recommended(dest CleanupDest) CleanupRequest {
	req := CleanupRequest{Dest: dest, Groups: []CleanupGroup{}}
	for _, g := range r.Groups {
		var cg CleanupGroup
		for _, m := range g.Members {
			it := CleanupItem{m.Path, m.Size, m.Modified}
			if m.Keep {
				cg.Keep = append(cg.Keep, it)
			} else {
				cg.Remove = append(cg.Remove, it)
			}
		}
		req.Groups = append(req.Groups, cg)
	}
	return req
}

func unchanged(i CleanupItem) (os.FileInfo, error) {
	fi, err := os.Lstat(i.Path)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", i.Path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is no longer a regular file", i.Path)
	}
	if uint64(fi.Size()) != i.Size || fi.ModTime().Unix() != i.Modified {
		return nil, fmt.Errorf("%s changed since the scan", i.Path)
	}
	return fi, nil
}

func freeName(dir, name string, taken []string) string {
	for n := 1; ; n++ {
		c := name
		if n > 1 {
			if i := strings.LastIndexByte(name, '.'); i > 0 {
				c = fmt.Sprintf("%s (%d)%s", name[:i], n, name[i:])
			} else {
				c = fmt.Sprintf("%s (%d)", name, n)
			}
		}
		p := filepath.Join(dir, c)
		if _, err := os.Lstat(p); err != nil && !slices.Contains(taken, p) {
			return p
		}
	}
}

// PlanCleanup is the dry run: what would move where, and what is left
// alone and why.
func (ld *Laserdot) PlanCleanup(req CleanupRequest) CleanupPlan {
	plan := CleanupPlan{Moves: []PlannedMove{}, Skipped: []string{}}
	if req.Dest.To == "folder" {
		if fi, err := os.Stat(req.Dest.Folder); err != nil || !fi.IsDir() {
			plan.Skipped = append(plan.Skipped, req.Dest.Folder+" is not a folder")
			return plan
		}
	} else if req.Dest.To != "trash" {
		plan.Skipped = append(plan.Skipped, fmt.Sprintf("unknown destination %q", req.Dest.To))
		return plan
	}
	var taken []string
	for _, g := range req.Groups {
		if len(g.Keep) == 0 {
			plan.Skipped = append(plan.Skipped, "a group with no copy to keep was left alone")
			continue
		}
		var kept []os.FileInfo
		var why error
		for _, k := range g.Keep {
			fi, err := unchanged(k)
			if err != nil {
				why = err
				break
			}
			kept = append(kept, fi)
		}
		if why != nil {
			plan.Skipped = append(plan.Skipped, "group left alone: "+why.Error())
			continue
		}
		for _, r := range g.Remove {
			if slices.ContainsFunc(g.Keep, func(k CleanupItem) bool { return k.Path == r.Path }) {
				plan.Skipped = append(plan.Skipped, r.Path+" is also marked to keep")
				continue
			}
			fi, err := unchanged(r)
			if err != nil {
				plan.Skipped = append(plan.Skipped, err.Error())
				continue
			}
			if slices.ContainsFunc(kept, func(k os.FileInfo) bool { return os.SameFile(k, fi) }) {
				plan.Skipped = append(plan.Skipped, r.Path+" is the kept file itself (a hard link)")
				continue
			}
			if slices.ContainsFunc(plan.Moves, func(m PlannedMove) bool { return m.From == r.Path }) {
				continue
			}
			mv := PlannedMove{From: r.Path, Size: r.Size}
			if req.Dest.To == "folder" {
				p := freeName(req.Dest.Folder, filepath.Base(r.Path), taken)
				taken = append(taken, p)
				mv.To = &p
			}
			plan.Bytes += r.Size
			plan.Moves = append(plan.Moves, mv)
		}
	}
	return plan
}

// ApplyCleanup moves the chosen copies (re-checking everything first) and
// updates the index.
func (ld *Laserdot) ApplyCleanup(req CleanupRequest) (CleanupResult, error) {
	plan := ld.PlanCleanup(req)
	res := CleanupResult{Moved: []PlannedMove{}, Skipped: plan.Skipped, Failed: []string{}}
	var touched []string
	for _, mv := range plan.Moves {
		var to string
		var err error
		if mv.To == nil {
			to, err = scan.Trash(mv.From)
		} else {
			to, err = *mv.To, moveFile(mv.From, *mv.To)
		}
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", mv.From, err))
			continue
		}
		touched = append(touched, filepath.Dir(mv.From))
		if to != "" {
			touched = append(touched, filepath.Dir(to))
			mv.To = &to
		}
		res.Bytes += mv.Size
		res.Moved = append(res.Moved, mv)
	}
	slices.Sort(touched)
	touched = slices.Compact(touched)
	var changes []Change
	for _, p := range touched {
		changes = append(changes, Change{Path: p})
	}
	if len(changes) > 0 {
		if _, err := ld.ApplyChanges(changes); err != nil {
			return res, err
		}
	}
	return res, nil
}

// moveFile renames, or copies then removes across volumes. It never
// overwrites.
func moveFile(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s exists", to)
	}
	err := os.Rename(from, to)
	var le *os.LinkError
	if err == nil || !errors.As(err, &le) || !errors.Is(le.Err, syscall.EXDEV) {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	out.Close()
	fi, serr := os.Stat(from)
	if err != nil || serr != nil || n != fi.Size() {
		os.Remove(to)
		if err == nil {
			err = errors.New("copy was incomplete")
		}
		return err
	}
	return os.Remove(from)
}
