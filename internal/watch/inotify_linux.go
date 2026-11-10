package watch

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"github.com/jamalxcode/laserdot-go/internal/scan"
	"golang.org/x/sys/unix"
)

const inotifyMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF |
	unix.IN_ONLYDIR

// inotifyWatcher keeps one watch per folder, in step with folder creation,
// renames and deletion.
type inotifyWatcher struct {
	fd       int
	wake     [2]int // pipe: Close wakes the poll at once
	roots    []string
	scanOpts scan.Options
	paths    map[int]string // watch descriptor → folder
	q        *queue
	wg       sync.WaitGroup
}

func startNative(roots []WatchRoot, _ Options, scanOpts scan.Options, q *queue) (native, Backend, bool, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, Poll, false, err
	}
	w := &inotifyWatcher{fd: fd, scanOpts: scanOpts, paths: map[int]string{}, q: q}
	if err := unix.Pipe2(w.wake[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		unix.Close(fd)
		return nil, Poll, false, err
	}
	for _, r := range roots {
		w.roots = append(w.roots, r.Path)
		if err := w.addTree(r.Path); err != nil {
			w.closeFDs()
			return nil, Poll, false, err
		}
	}
	w.wg.Add(1)
	go w.run()
	return w, Inotify, false, nil
}

func (w *inotifyWatcher) closeFDs() {
	unix.Close(w.fd)
	unix.Close(w.wake[0])
	unix.Close(w.wake[1])
}

func (w *inotifyWatcher) stop() {
	_, _ = unix.Write(w.wake[1], []byte{0})
	w.wg.Wait()
	w.closeFDs()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// addTree watches dir and every folder below it (respecting the ignore
// rules, and not crossing into another volume). Only running out of
// watches (ENOSPC) is an error; folders that vanish or cannot be read are
// skipped.
func (w *inotifyWatcher) addTree(dir string) error {
	var rootDev uint64
	if st, err := os.Stat(dir); err == nil {
		rootDev = devOf(st)
	}
	stack := []string{dir}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		wd, err := unix.InotifyAddWatch(w.fd, d, inotifyMask)
		if err != nil {
			if errors.Is(err, unix.ENOSPC) {
				return err
			}
			continue
		}
		w.paths[wd] = d
		if scan.IsOpaque(d, w.scanOpts) {
			continue
		}
		des, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, de := range des {
			if !de.IsDir() || contains(w.scanOpts.IgnoredDirs, de.Name()) {
				continue
			}
			p := filepath.Join(d, de.Name())
			if st, err := os.Lstat(p); err == nil && rootDev != 0 && devOf(st) != rootDev {
				continue // another volume mounted inside the tree
			}
			stack = append(stack, p)
		}
	}
	return nil
}

func devOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}

// renamed rewrites the paths of a renamed watched folder and those below.
func (w *inotifyWatcher) renamed(from, to string) {
	for wd, p := range w.paths {
		if isUnder(p, from) {
			w.paths[wd] = to + p[len(from):]
		}
	}
}

// forget drops the watches of a folder moved out of the watched trees.
func (w *inotifyWatcher) forget(dir string) {
	for wd, p := range w.paths {
		if isUnder(p, dir) {
			_, _ = unix.InotifyRmWatch(w.fd, uint32(wd))
			delete(w.paths, wd)
		}
	}
}

func (w *inotifyWatcher) run() {
	defer w.wg.Done()
	buf := make([]byte, 64*1024)
	fds := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}, {Fd: int32(w.wake[0]), Events: unix.POLLIN}}
	for {
		fds[0].Revents, fds[1].Revents = 0, 0
		n, err := unix.Poll(fds, -1)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		if n <= 0 {
			continue
		}
		if fds[1].Revents != 0 {
			return
		}
		got, err := unix.Read(w.fd, buf)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		w.handle(buf[:got])
	}
}

// handle processes one read's worth of events.
func (w *inotifyWatcher) handle(b []byte) {
	movedFrom := map[uint32]string{}
	for len(b) >= unix.SizeofInotifyEvent {
		ev := (*unix.InotifyEvent)(unsafe.Pointer(&b[0]))
		size := unix.SizeofInotifyEvent + int(ev.Len)
		if size > len(b) {
			break
		}
		name := ""
		if ev.Len > 0 {
			raw := b[unix.SizeofInotifyEvent:size]
			for i, c := range raw {
				if c == 0 {
					raw = raw[:i]
					break
				}
			}
			name = string(raw)
		}
		b = b[size:]
		mask := ev.Mask
		if mask&unix.IN_Q_OVERFLOW != 0 {
			// Events were lost, folder creations among them: rescan and
			// re-watch every root.
			for _, r := range w.roots {
				_ = w.addTree(r)
				w.q.changed(r, true)
			}
			continue
		}
		wd := int(ev.Wd)
		if mask&unix.IN_IGNORED != 0 {
			delete(w.paths, wd)
			continue
		}
		dir, ok := w.paths[wd]
		if !ok {
			continue
		}
		if mask&unix.IN_ISDIR != 0 && name != "" {
			child := filepath.Join(dir, name)
			switch {
			case mask&unix.IN_MOVED_FROM != 0:
				movedFrom[ev.Cookie] = child
			case mask&unix.IN_MOVED_TO != 0:
				if from, ok := movedFrom[ev.Cookie]; ok {
					delete(movedFrom, ev.Cookie)
					w.renamed(from, child)
				} else if !contains(w.scanOpts.IgnoredDirs, name) {
					_ = w.addTree(child)
				}
			case mask&unix.IN_CREATE != 0:
				if !contains(w.scanOpts.IgnoredDirs, name) && !scan.IsOpaque(dir, w.scanOpts) {
					_ = w.addTree(child)
				}
			}
		}
		w.q.changed(dir, false)
	}
	// Folders moved out of the watched trees (no matching MOVED_TO).
	for _, from := range movedFrom {
		w.forget(from)
	}
}
