package main

import (
	"encoding/json"
	"path/filepath"

	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/ipc"
	"github.com/jamalxcode/laserdot-go/internal/media"
)

// target is where commands go: the running daemon (the index's single
// writer), or the index opened directly when no daemon runs.
type target interface {
	search(q string, page index.Page, typo bool) (*core.SearchResults, error)
	index(path string) (core.IndexStats, error)
	refresh() (core.UpdateStats, error)
	forget(path string) (bool, error)
	roots() ([]index.RootInfo, error)
	stats() (core.Stats, error)
	close()
	moreTarget
}

// open picks the daemon when one answers (unless local), else the index.
func open(db string, local bool) (target, error) {
	if !local {
		if c := ipc.ConnectLive(ipc.SocketFor(db)); c != nil {
			return &remote{c}, nil
		}
	}
	ld, err := core.Open(db)
	if err != nil {
		return nil, err
	}
	return &direct{ld}, nil
}

type direct struct{ ld *core.Laserdot }

func (d *direct) search(q string, page index.Page, typo bool) (*core.SearchResults, error) {
	return d.ld.Search(q, core.SearchOptions{Page: page, TypoFallback: typo})
}
func (d *direct) index(p string) (core.IndexStats, error) { return d.ld.Index(p) }
func (d *direct) refresh() (core.UpdateStats, error)      { return d.ld.Refresh() }
func (d *direct) forget(p string) (bool, error)           { return d.ld.Forget(p) }
func (d *direct) roots() ([]index.RootInfo, error)        { return d.ld.Roots() }
func (d *direct) stats() (core.Stats, error)              { return d.ld.Stats(), nil }
func (d *direct) close()                                  { d.ld.Close() }

type remote struct{ c *ipc.Client }

func (r *remote) search(q string, page index.Page, typo bool) (*core.SearchResults, error) {
	var res core.SearchResults
	limit := page.Limit
	err := r.c.Call(&ipc.Request{Op: ipc.OpSearch, Query: q, Sort: page.Sort.String(), Reverse: page.Reverse,
		Offset: page.Offset, Limit: &limit, Typo: &typo}, &res)
	return &res, err
}

func (r *remote) index(p string) (core.IndexStats, error) {
	var st core.IndexStats
	return st, r.c.Call(&ipc.Request{Op: ipc.OpIndex, Path: p}, &st)
}

func (r *remote) refresh() (core.UpdateStats, error) {
	var u core.UpdateStats
	return u, r.c.Call(&ipc.Request{Op: ipc.OpRefresh}, &u)
}

func (r *remote) forget(p string) (bool, error) {
	var ok bool
	return ok, r.c.Call(&ipc.Request{Op: ipc.OpForget, Path: p}, &ok)
}

func (r *remote) roots() ([]index.RootInfo, error) {
	var out []index.RootInfo
	return out, r.c.Call(&ipc.Request{Op: ipc.OpRoots}, &out)
}

func (r *remote) stats() (core.Stats, error) {
	var s core.Stats
	return s, r.c.Call(&ipc.Request{Op: ipc.OpStats}, &s)
}

func (r *remote) close() { r.c.Close() }

// More operations, through the same two routes.
type moreTarget interface {
	drives() ([]core.DriveInfo, error)
	drivePlan(mount string) (core.DrivePlan, error)
	driveIndex(mount, pass string) (core.IndexStats, error)
	driveAttach(mount, pass string) (core.DriveInfo, error)
	driveDetach(mount string) (bool, error)
	driveRebuild(mount, pass string) (core.IndexStats, error)
	driveDelete(mount string) (bool, error)
	driveEject(mount string) error
	dupes(o core.DupeOptions) (*core.DupeReport, error)
	cleanupPlan(r core.CleanupRequest) (core.CleanupPlan, error)
	cleanup(r core.CleanupRequest) (core.CleanupResult, error)
	contentStatus() (core.ContentStatus, error)
	setContent(s core.ContentSettings) (core.ContentStatus, error)
	updateContent() (core.ContentStats, error)
	coach(q string) ([]core.Coaching, error)
	deep(limit int) (core.DeepStats, error)
	loudness(path string) (media.Loudness, error)
}

func (d *direct) deep(limit int) (core.DeepStats, error)    { return d.ld.DeepProbe(limit) }
func (d *direct) loudness(p string) (media.Loudness, error) { return d.ld.Loudness(p) }
func (r *remote) deep(limit int) (core.DeepStats, error) {
	return call[core.DeepStats](r, &ipc.Request{Op: ipc.OpDeepProbe, Limit: &limit})
}
func (r *remote) loudness(p string) (media.Loudness, error) {
	abs, _ := filepath.Abs(p)
	return call[media.Loudness](r, &ipc.Request{Op: ipc.OpLoudness, Path: abs})
}

func (d *direct) drives() ([]core.DriveInfo, error)                  { return d.ld.Drives(), nil }
func (d *direct) drivePlan(m string) (core.DrivePlan, error)         { return d.ld.PlanDrive(m) }
func (d *direct) driveDetach(m string) (bool, error)                 { return d.ld.DetachDrive(m), nil }
func (d *direct) driveDelete(m string) (bool, error)                 { return d.ld.DeleteDriveIndex(m) }
func (d *direct) driveEject(m string) error                          { return d.ld.EjectDrive(m) }
func (d *direct) dupes(o core.DupeOptions) (*core.DupeReport, error) { return d.ld.FindDuplicates(o) }
func (d *direct) cleanupPlan(r core.CleanupRequest) (core.CleanupPlan, error) {
	return d.ld.PlanCleanup(r), nil
}
func (d *direct) cleanup(r core.CleanupRequest) (core.CleanupResult, error) {
	return d.ld.ApplyCleanup(r)
}
func (d *direct) contentStatus() (core.ContentStatus, error) { return d.ld.ContentStatus() }
func (d *direct) updateContent() (core.ContentStats, error)  { return d.ld.UpdateContent() }
func (d *direct) coach(q string) ([]core.Coaching, error)    { return d.ld.Coach(q), nil }
func (d *direct) driveIndex(m, p string) (core.IndexStats, error) {
	return d.ld.CreateDriveIndex(m, core.DriveOptions{Passphrase: p})
}
func (d *direct) driveAttach(m, p string) (core.DriveInfo, error) { return d.ld.AttachDrive(m, p) }
func (d *direct) driveRebuild(m, p string) (core.IndexStats, error) {
	return d.ld.RebuildDrive(m, core.DriveOptions{Passphrase: p})
}
func (d *direct) setContent(s core.ContentSettings) (core.ContentStatus, error) {
	if err := d.ld.SetContentSettings(s); err != nil {
		return core.ContentStatus{}, err
	}
	return d.ld.ContentStatus()
}

func call[T any](r *remote, req *ipc.Request) (T, error) {
	var out T
	return out, r.c.Call(req, &out)
}

func passPtr(p string) *string {
	if p == "" {
		return nil
	}
	return &p
}

func rawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (r *remote) drives() ([]core.DriveInfo, error) {
	return call[[]core.DriveInfo](r, &ipc.Request{Op: ipc.OpDrives})
}
func (r *remote) drivePlan(m string) (core.DrivePlan, error) {
	return call[core.DrivePlan](r, &ipc.Request{Op: ipc.OpDrivePlan, Mount: m})
}
func (r *remote) driveIndex(m, p string) (core.IndexStats, error) {
	return call[core.IndexStats](r, &ipc.Request{Op: ipc.OpDriveIndex, Mount: m, Passphrase: passPtr(p)})
}
func (r *remote) driveAttach(m, p string) (core.DriveInfo, error) {
	return call[core.DriveInfo](r, &ipc.Request{Op: ipc.OpDriveAttach, Mount: m, Passphrase: passPtr(p)})
}
func (r *remote) driveDetach(m string) (bool, error) {
	return call[bool](r, &ipc.Request{Op: ipc.OpDriveDetach, Mount: m})
}
func (r *remote) driveRebuild(m, p string) (core.IndexStats, error) {
	return call[core.IndexStats](r, &ipc.Request{Op: ipc.OpDriveRebuild, Mount: m, Passphrase: passPtr(p)})
}
func (r *remote) driveDelete(m string) (bool, error) {
	return call[bool](r, &ipc.Request{Op: ipc.OpDriveDelete, Mount: m})
}
func (r *remote) driveEject(m string) error {
	_, err := call[string](r, &ipc.Request{Op: ipc.OpDriveEject, Mount: m})
	return err
}
func (r *remote) dupes(o core.DupeOptions) (*core.DupeReport, error) {
	rep, err := call[core.DupeReport](r, &ipc.Request{Op: ipc.OpDupes, Options: rawJSON(o)})
	return &rep, err
}
func (r *remote) cleanupPlan(c core.CleanupRequest) (core.CleanupPlan, error) {
	return call[core.CleanupPlan](r, &ipc.Request{Op: ipc.OpCleanupPlan, Request: rawJSON(c)})
}
func (r *remote) cleanup(c core.CleanupRequest) (core.CleanupResult, error) {
	return call[core.CleanupResult](r, &ipc.Request{Op: ipc.OpCleanup, Request: rawJSON(c)})
}
func (r *remote) contentStatus() (core.ContentStatus, error) {
	return call[core.ContentStatus](r, &ipc.Request{Op: ipc.OpContentStatus})
}
func (r *remote) setContent(s core.ContentSettings) (core.ContentStatus, error) {
	return call[core.ContentStatus](r, &ipc.Request{Op: ipc.OpSetContentSettings, Settings: rawJSON(s)})
}
func (r *remote) updateContent() (core.ContentStats, error) {
	return call[core.ContentStats](r, &ipc.Request{Op: ipc.OpUpdateContent})
}
func (r *remote) coach(q string) ([]core.Coaching, error) {
	return call[[]core.Coaching](r, &ipc.Request{Op: ipc.OpCoach, Query: q})
}
