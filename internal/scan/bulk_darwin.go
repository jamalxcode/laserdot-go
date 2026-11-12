//go:build darwin

package scan

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// macOS reads a folder's entries with their attributes in bulk:
// getattrlistbulk(2) returns names, types, sizes, dates, inode and flags for
// many children per call, where reading names and then lstat-ing each child
// costs a system call per file (the larger share of a walk). It is bound at
// run time with purego (no cgo). Anything unexpected — the call missing, an
// error, a mount point — falls back to the name-by-name way for that folder
// or entry, which gives identical entries (TestBulkMatchesStat).

const (
	attrBitMapCount = 5

	attrCmnName          = 0x00000001
	attrCmnDevID         = 0x00000002
	attrCmnObjType       = 0x00000008
	attrCmnCrTime        = 0x00000200
	attrCmnModTime       = 0x00000400
	attrCmnFlags         = 0x00040000
	attrCmnFileID        = 0x02000000
	attrCmnReturnedAttrs = 0x80000000

	attrDirMountStatus = 0x00000004
	dirMntStatusMntPnt = 0x00000001

	attrFileLinkCount  = 0x00000001
	attrFileDataLength = 0x00000200

	vDIR = 2
	vLNK = 5

	bulkBufSize = 256 << 10
)

// attrList is struct attrlist.
type attrList struct {
	bitmapCount uint16
	reserved    uint16
	commonAttr  uint32
	volAttr     uint32
	dirAttr     uint32
	fileAttr    uint32
	forkAttr    uint32
}

var bulkRequest = attrList{
	bitmapCount: attrBitMapCount,
	commonAttr: attrCmnReturnedAttrs | attrCmnName | attrCmnDevID | attrCmnObjType | attrCmnCrTime | attrCmnModTime |
		attrCmnFlags | attrCmnFileID,
	dirAttr:  attrDirMountStatus,
	fileAttr: attrFileLinkCount | attrFileDataLength,
}

var (
	bulkOnce sync.Once
	bulkCall func(fd int32, list unsafe.Pointer, buf unsafe.Pointer, size uintptr, options uint64) int32
	bulkBufs = sync.Pool{New: func() any { b := make([]byte, bulkBufSize); return &b }}
)

func bulkAvailable() bool {
	bulkOnce.Do(func() {
		if os.Getenv("LASERDOT_NO_BULK") != "" {
			return // measurements and tests: the name-by-name way
		}
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		sym, err := purego.Dlsym(lib, "getattrlistbulk")
		if err != nil {
			return
		}
		purego.RegisterFunc(&bulkCall, sym)
	})
	return bulkCall != nil
}

// readDirBulk lists dir in bulk. ok is false when the bulk call cannot be
// used for this folder (the caller then lists it name by name).
func readDirBulk(dir string, parent int, opts Options) ([]Entry, []Error, bool) {
	if !bulkAvailable() {
		return nil, nil, false
	}
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		// The same report as reading the names would give.
		return nil, []Error{{Path: dir, Message: (&os.PathError{Op: "open", Path: dir, Err: err}).Error()}}, true
	}
	defer syscall.Close(fd)
	bp := bulkBufs.Get().(*[]byte)
	defer bulkBufs.Put(bp)
	buf := *bp
	req := bulkRequest
	var out []Entry
	var errs []Error
	for {
		n := bulkCall(int32(fd), unsafe.Pointer(&req), unsafe.Pointer(&buf[0]), uintptr(len(buf)), 0)
		if n < 0 {
			return nil, nil, false
		}
		if n == 0 {
			break
		}
		at := 0
		for i := int32(0); i < n; i++ {
			if at+4 > len(buf) {
				return nil, nil, false
			}
			length := int(binary.LittleEndian.Uint32(buf[at:]))
			if length <= 0 || at+length > len(buf) {
				return nil, nil, false
			}
			e, name, ok := parseBulkEntry(buf[at : at+length])
			at += length
			if !ok {
				return nil, nil, false
			}
			if name == "." || name == ".." {
				continue
			}
			path := filepath.Join(dir, name)
			if skip(dir, name, path, e.IsDir, opts) {
				continue
			}
			if e.mountPoint || e.IsSymlink {
				// Another volume is mounted here (lstat gives its device, so
				// the walk stops there), or a symlink (rare; lstat gives the
				// size the name-by-name way reports): ask lstat.
				info, err := os.Lstat(path)
				if err != nil {
					errs = append(errs, Error{Path: path, Message: err.Error()})
					continue
				}
				out = append(out, entryOf(path, parent, info, opts))
				continue
			}
			entry := e.Entry
			entry.Path, entry.Parent = path, parent
			if !opts.StableInodes {
				entry.Inode = 0
			}
			if !entry.IsSymlink && !opts.DeferTags {
				entry.Tags = ReadTags(path)
			}
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, errs, true
}

type bulkEntry struct {
	Entry
	mountPoint bool
}

// parseBulkEntry reads one packed entry (its first 4 bytes are its length).
// After the length comes the set of attributes actually returned (5×u32:
// common, volume, folder, file, fork); each requested attribute follows
// only if its bit is set there, in bit order: name (attrreference: i32
// offset from itself, u32 length with the NUL); device (i32); object type
// (u32); birth and modification times (timespec: i64 s, i64 ns); flags
// (u32); file id (u64); then mount status (u32, folders only); then link
// count (u32) and data length (i64, files only).
func parseBulkEntry(b []byte) (bulkEntry, string, bool) {
	le := binary.LittleEndian
	p := 4
	need := func(n int) bool { return p+n <= len(b) }
	if !need(20) {
		return bulkEntry{}, "", false
	}
	common, dirSet, fileSet := le.Uint32(b[p:]), le.Uint32(b[p+8:]), le.Uint32(b[p+12:])
	p += 20
	// Name, type and file id are needed; without them, list this folder name by name.
	if common&attrCmnName == 0 || common&attrCmnObjType == 0 || common&attrCmnModTime == 0 {
		return bulkEntry{}, "", false
	}
	e := bulkEntry{}
	var name string
	if !need(8) {
		return bulkEntry{}, "", false
	}
	nameRef := p
	nameOff := int(int32(le.Uint32(b[p:])))
	nameLen := int(le.Uint32(b[p+4:]))
	p += 8
	start := nameRef + nameOff
	if nameLen < 1 || start < 0 || start+nameLen > len(b) {
		return bulkEntry{}, "", false
	}
	name = string(b[start : start+nameLen-1]) // without the NUL
	if common&attrCmnDevID != 0 {
		if !need(4) {
			return bulkEntry{}, "", false
		}
		e.Dev = uint64(int32(le.Uint32(b[p:]))) // as statOf converts st_dev (sign-extended)
		p += 4
	}
	if !need(4) {
		return bulkEntry{}, "", false
	}
	objType := le.Uint32(b[p:])
	p += 4
	e.IsDir = objType == vDIR
	e.IsSymlink = objType == vLNK
	if common&attrCmnCrTime != 0 {
		if !need(16) {
			return bulkEntry{}, "", false
		}
		e.Created = int64(le.Uint64(b[p:]))
		p += 16
	}
	if !need(16) {
		return bulkEntry{}, "", false
	}
	e.Modified = int64(le.Uint64(b[p:]))
	p += 16
	if e.Created == 0 {
		e.Created = e.Modified
	}
	if common&attrCmnFlags != 0 {
		if !need(4) {
			return bulkEntry{}, "", false
		}
		e.Dataless = le.Uint32(b[p:])&sfDataless != 0
		p += 4
	}
	if common&attrCmnFileID != 0 {
		if !need(8) {
			return bulkEntry{}, "", false
		}
		e.Inode = le.Uint64(b[p:])
		p += 8
	}
	if dirSet&attrDirMountStatus != 0 {
		if !need(4) {
			return bulkEntry{}, "", false
		}
		e.mountPoint = le.Uint32(b[p:])&dirMntStatusMntPnt != 0
		p += 4
	}
	if !e.IsDir {
		if fileSet&attrFileLinkCount != 0 {
			if !need(4) {
				return bulkEntry{}, "", false
			}
			e.Nlink = uint64(le.Uint32(b[p:]))
			p += 4
		}
		if fileSet&attrFileDataLength != 0 {
			if !need(8) {
				return bulkEntry{}, "", false
			}
			e.Size = uint64(le.Uint64(b[p:]))
			p += 8
		} else if !e.IsSymlink {
			return bulkEntry{}, "", false // a file without its size: ask lstat
		}
	}
	return e, name, true
}
