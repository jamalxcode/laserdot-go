// Package scan is Tier 0 (spec §3.1): walk a folder tree and collect names
// and metadata. Directories are read in parallel; symlinks are recorded,
// never followed (so there are no cycles to detect); hard links are listed
// once (§2.6.5); half-downloaded files are skipped until complete (story 12);
// another volume mounted inside the tree is not entered (§17.6).
package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultIgnoredDirs are never indexed (spec §13.6), matched exactly.
var DefaultIgnoredDirs = []string{
	"node_modules", ".venv", "__pycache__", ".cache", ".Trash", ".Trashes", ".Spotlight-V100",
	".fseventsd", ".DocumentRevisions-V100", ".TemporaryItems",
	".laserdot", // our own on-drive index (§7.7)
}

// DefaultOpaqueDirs are listed as results, but their contents are not indexed.
var DefaultOpaqueDirs = []string{".git", ".hg", ".svn"}

// DefaultOpaqueExts: folders with these extensions are macOS packages (an app
// is a folder of thousands of files). Like Finder and Spotlight, Laserdot lists
// the package as one result and does not index its contents. Photos and Music
// libraries are personal-data stores (spec pillar 4), so they stay closed too.
var DefaultOpaqueExts = []string{
	"app", "appex", "framework", "bundle", "plugin", "kext", "xpc", "prefpane", "qlgenerator",
	"mdimporter", "saver", "photoslibrary", "musiclibrary", "photolibrary",
}

// PartialDownloadExts are downloads in progress: never indexed (story 12).
var PartialDownloadExts = []string{"crdownload", "part", "partial", "download", "opdownload", "!qb", "!ut"}

// IsPartialDownload reports a download still in progress, by extension.
func IsPartialDownload(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	ext := name[i+1:]
	for _, p := range PartialDownloadExts {
		if strings.EqualFold(ext, p) {
			return true
		}
	}
	return false
}

// Options control a walk.
type Options struct {
	// Found, when set, counts entries as Walk lists them (indexing
	// progress: how many files were found so far).
	Found       *atomic.Uint64
	IgnoredDirs []string
	OpaqueDirs  []string
	// OpaqueExts: package folders by extension (lowercase, no dot).
	OpaqueExts []string
	// ExcludedPaths are never indexed (e.g. the index database itself).
	ExcludedPaths []string
	// OnlyChildren: in these folders, only the named children are indexed
	// (~/Library: only Mobile Documents, i.e. iCloud Drive).
	OnlyChildren map[string][]string
	// StableInodes: the volume's inode numbers survive remounts. When not
	// (FAT, exFAT) every inode is reported as 0.
	StableInodes bool
	// DeferTags: leave Tags empty (a first index reads them afterwards, in
	// the background: one extended-attribute read per file is a large share
	// of a walk on macOS, and few files have tags).
	DeferTags bool
}

// DefaultOptions are the spec's defaults.
func DefaultOptions() Options {
	return Options{IgnoredDirs: DefaultIgnoredDirs, OpaqueDirs: DefaultOpaqueDirs, OpaqueExts: DefaultOpaqueExts, StableInodes: true}
}

// Entry is one file system entry.
type Entry struct {
	Path string
	// Parent is the index of the parent entry in the walk's output (-1 for
	// the root).
	Parent    int
	IsDir     bool
	IsSymlink bool
	Size      uint64
	Modified  int64 // Unix seconds
	Created   int64 // birth time where available, else Modified
	Inode     uint64
	Dev       uint64
	Nlink     uint64
	// Dataless: an iCloud placeholder whose data is not on disk (§11). Never
	// open these: reading one triggers a download.
	Dataless bool
	// Tags: Finder tags (macOS) or user.xdg.tags (Linux), colour suffixes
	// removed (ReadTags).
	Tags []string
}

// Error is an unreadable entry: reported, not fatal.
type Error struct {
	Path    string
	Message string
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Walk walks root. Entries come parent-first, so Parent always points
// backwards.
func Walk(root string, opts Options) ([]Entry, []Error, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, nil, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, nil, err
	}
	rootEntry := entryOf(root, -1, rootInfo, opts)
	entries := []Entry{rootEntry}
	var errs []Error
	if !rootEntry.IsDir {
		return entries, nil, nil
	}
	// Level by level: every directory of a level is read in parallel, and
	// its children are appended in name order, so output is deterministic.
	level := []int{0}
	// Reading folders waits on the disk, not the CPU: more readers than cores.
	workers := max(8, runtime.GOMAXPROCS(0)*4)
	for len(level) > 0 {
		results := make([][]Entry, len(level))
		failures := make([][]Error, len(level))
		var wg sync.WaitGroup
		next := make(chan int)
		for w := 0; w < min(workers, len(level)); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range next {
					dir := entries[level[i]]
					results[i], failures[i], _ = readDirWithin(dir.Path, level[i], rootEntry.Dev, opts)
					if opts.Found != nil {
						opts.Found.Add(uint64(len(results[i])))
					}
				}
			}()
		}
		for i := range level {
			next <- i
		}
		close(next)
		wg.Wait()
		var nextLevel []int
		// One allocation per level, not repeated doubling (a million entries
		// would otherwise be copied many times over).
		total := 0
		for i := range level {
			total += len(results[i])
		}
		entries = slices.Grow(entries, total)
		for i := range level {
			errs = append(errs, failures[i]...)
			for _, e := range results[i] {
				if e.IsDir && !IsOpaque(e.Path, opts) && e.Dev == rootEntry.Dev {
					nextLevel = append(nextLevel, len(entries))
				}
				entries = append(entries, e)
			}
		}
		level = nextLevel
	}
	return dedupeHardLinks(entries), errs, nil
}

// readDir lists dir's children with the ignore rules applied: in bulk where
// the system offers it (macOS, readDirBulk), else name by name.
func readDir(dir string, parent int, rootDev uint64, opts Options) ([]Entry, []Error) {
	if readDirHook != nil {
		readDirHook(dir)
	}
	if out, errs, ok := readDirBulk(dir, parent, opts); ok {
		return out, errs
	}
	return readDirStat(dir, parent, opts)
}

// skip: the ignore rules for a child of dir.
func skip(dir, name, path string, isDir bool, opts Options) bool {
	only, restricted := opts.OnlyChildren[dir]
	return (isDir && contains(opts.IgnoredDirs, name)) || IsPartialDownload(name) || contains(opts.ExcludedPaths, path) ||
		(restricted && !contains(only, name))
}

// readDirStat lists dir by reading its names, then one lstat per child.
func readDirStat(dir string, parent int, opts Options) ([]Entry, []Error) {
	des, err := os.ReadDir(dir)
	if err != nil && len(des) == 0 {
		return nil, []Error{{Path: dir, Message: err.Error()}}
	}
	var out []Entry
	var errs []Error
	for _, de := range des {
		name := de.Name()
		path := filepath.Join(dir, name)
		if skip(dir, name, path, de.IsDir(), opts) {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			errs = append(errs, Error{Path: path, Message: err.Error()})
			continue
		}
		out = append(out, entryOf(path, parent, info, opts))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, errs
}

// ListDir lists one directory's direct children (for incremental rescans)
// with the same rules as Walk. Parent is -1 on every entry.
func ListDir(dir string, opts Options) ([]Entry, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	out, _, stuck := readDirWithin(dir, -1, 0, opts)
	if stuck {
		// Not "empty": the folder's entries must stay as they are.
		return nil, errStuck(dir)
	}
	return out, nil
}

// FolderLimit: how long reading one folder may take. A disconnected or
// stuck drive (a dead network share) can make it wait forever; the folder is
// then skipped and reported instead, and indexing goes on.
var FolderLimit = 30 * time.Second

func errStuck(dir string) error {
	return fmt.Errorf("%s did not answer within %v (a disconnected or stuck drive?): skipped", dir, FolderLimit)
}

// readDirWithin is readDir with FolderLimit. stuck: it did not answer in
// time (the read goes on by itself and its result is dropped).
func readDirWithin(dir string, parent int, rootDev uint64, opts Options) ([]Entry, []Error, bool) {
	type listed struct {
		out  []Entry
		errs []Error
	}
	ch := make(chan listed, 1)
	go func() {
		out, errs := readDir(dir, parent, rootDev, opts)
		ch <- listed{out, errs}
	}()
	t := time.NewTimer(FolderLimit)
	defer t.Stop()
	select {
	case l := <-ch:
		return l.out, l.errs, false
	case <-t.C:
		return nil, []Error{{Path: dir, Message: errStuck(dir).Error()}}, true
	}
}

// readDirHook: tests make a folder hang.
var readDirHook func(dir string)

// IsOpaque: a version-control folder or a package (an app…) whose contents
// are not indexed.
func IsOpaque(dir string, opts Options) bool {
	name := filepath.Base(dir)
	if contains(opts.OpaqueDirs, name) {
		return true
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		ext := strings.ToLower(name[i+1:])
		for _, e := range opts.OpaqueExts {
			if ext == e {
				return true
			}
		}
	}
	return false
}

// IsMountPoint: another volume is mounted at dir (its device differs from
// its parent folder's).
func IsMountPoint(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	parent, err := os.Stat(filepath.Dir(dir))
	if err != nil {
		return false
	}
	return devOf(info) != devOf(parent)
}

// dedupeHardLinks keeps one path per hard-linked file — the
// lexicographically smallest, so the choice is stable across walks.
func dedupeHardLinks(entries []Entry) []Entry {
	type key struct{ dev, ino uint64 }
	keep := map[key]int{}
	for i, e := range entries {
		if e.IsDir || e.Nlink < 2 {
			continue
		}
		k := key{e.Dev, e.Inode}
		if j, ok := keep[k]; !ok || e.Path < entries[j].Path {
			keep[k] = i
		}
	}
	if len(keep) == 0 {
		return entries
	}
	remap := make([]int, len(entries))
	out := make([]Entry, 0, len(entries))
	for i, e := range entries {
		if !e.IsDir && e.Nlink >= 2 && keep[key{e.Dev, e.Inode}] != i {
			remap[i] = -1
			continue
		}
		if e.Parent >= 0 {
			e.Parent = remap[e.Parent]
		}
		remap[i] = len(out)
		out = append(out, e)
	}
	return out
}

func entryOf(path string, parent int, info os.FileInfo, opts Options) Entry {
	st := statOf(path, info)
	e := Entry{
		Path:      path,
		Parent:    parent,
		IsDir:     info.IsDir(),
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
		Modified:  info.ModTime().Unix(),
		Created:   st.created,
		Inode:     st.inode,
		Dev:       st.dev,
		Nlink:     st.nlink,
		Dataless:  st.dataless,
	}
	if e.Created == 0 {
		e.Created = e.Modified
	}
	if !e.IsDir {
		e.Size = uint64(info.Size())
	}
	if !opts.StableInodes {
		e.Inode = 0
	}
	if !e.IsSymlink && !opts.DeferTags {
		e.Tags = ReadTags(path)
	}
	return e
}
