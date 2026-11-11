package core

import (
	"encoding/json"
	"io/fs"
	"sort"
	"strings"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// What a full index could not read (no permission, broken files), so the
// app can say so instead of leaving files silently missing. Kept per root,
// the first unreadableKeep of each, in the index's settings.
const (
	unreadableSetting = "unreadable"
	unreadableKeep    = 200
)

// UnreadableItem is one entry that could not be read.
type UnreadableItem struct {
	Path string `json:"path"`
	// Reason: "permission" (macOS privacy or file permissions) or "error".
	Reason string `json:"reason"`
	// Detail: the system's message.
	Detail string `json:"detail"`
}

// UnreadableReport lists them for the indexed folders.
type UnreadableReport struct {
	Total int              `json:"total"`
	Items []UnreadableItem `json:"items"`
}

type unreadableRoot struct {
	Total int              `json:"total"`
	Items []UnreadableItem `json:"items"`
}

func (ld *Laserdot) loadUnreadable() map[string]unreadableRoot {
	m := map[string]unreadableRoot{}
	if raw, err := ld.db.Setting(unreadableSetting); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

func reasonOf(msg string) string {
	l := strings.ToLower(msg)
	if strings.Contains(l, fs.ErrPermission.Error()) || strings.Contains(l, "operation not permitted") {
		return "permission"
	}
	return "error"
}

// noteUnreadable records the walk errors of a full index of root.
func (ld *Laserdot) noteUnreadable(root string, errs []scan.Error) {
	if root == "" {
		return
	}
	m := ld.loadUnreadable()
	if len(errs) == 0 {
		if _, had := m[root]; !had {
			return
		}
		delete(m, root)
	} else {
		r := unreadableRoot{Total: len(errs)}
		for _, e := range errs[:min(len(errs), unreadableKeep)] {
			r.Items = append(r.Items, UnreadableItem{Path: e.Path, Reason: reasonOf(e.Message), Detail: e.Message})
		}
		m[root] = r
	}
	if data, err := json.Marshal(m); err == nil {
		_ = ld.db.SetSetting(unreadableSetting, string(data))
	}
}

// Unreadable lists what the indexed folders' last full index could not
// read (folders no longer indexed are left out).
func (ld *Laserdot) Unreadable() (UnreadableReport, error) {
	roots, err := ld.db.Roots()
	if err != nil {
		return UnreadableReport{}, err
	}
	live := map[string]bool{}
	for _, r := range roots {
		live[r.Path] = true
	}
	var out UnreadableReport
	for root, r := range ld.loadUnreadable() {
		if !live[root] {
			continue
		}
		out.Total += r.Total
		out.Items = append(out.Items, r.Items...)
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Path < out.Items[j].Path })
	if out.Items == nil {
		out.Items = []UnreadableItem{}
	}
	return out, nil
}
