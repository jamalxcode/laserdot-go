package main

import (
	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/ipc"
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
