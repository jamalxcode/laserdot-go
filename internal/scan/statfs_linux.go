package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// fsInfo is what statfs says about the volume holding a path.
type fsInfo struct {
	fsType      string
	readOnly    bool
	total, free uint64
}

// fsMagic names Linux file systems by their statfs f_type.
var fsMagic = map[int64]string{
	0xEF53:                "ext4",
	0x58465342:            "xfs",
	0x9123683E:            "btrfs",
	0x01021994:            "tmpfs",
	0x794C7630:            "overlay",
	0x4D44:                "msdos",
	0x2011BAB0:            "exfat",
	0x5346544E:            "ntfs",
	0x7366746E:            "ntfs",
	0x6969:                "nfs",
	0x517B:                "smb",
	unix.CIFS_SUPER_MAGIC: "cifs",
	unix.SMB2_SUPER_MAGIC: "smb2",
	0x5346414F:            "afs",
	0x73757245:            "coda",
	0x01021997:            "9p",
	0x65735546:            "fuse",
	0xF15F:                "ecryptfs",
	0x2FC12FC1:            "zfs",
	0x9660:                "iso9660",
	0x15013346:            "udf",
	0x482B:                "hfsplus",
	0x62656572:            "sysfs",
	0x9FA0:                "proc",
	0x1CD1:                "devpts",
	0x64626720:            "debugfs",
}

func statFS(path string) (fsInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return fsInfo{}, err
	}
	name, ok := fsMagic[int64(st.Type)]
	if !ok {
		name = "unknown"
	}
	if name == "fuse" {
		// FUSE shares one magic number: the subtype (fuse.sshfs, …) is only
		// in the mount table.
		if t := mountType(path); t != "" {
			name = t
		}
	}
	return fsInfo{
		fsType:   name,
		readOnly: st.Flags&unix.ST_RDONLY != 0,
		total:    st.Blocks * uint64(st.Bsize),
		free:     st.Bavail * uint64(st.Bsize),
	}, nil
}

// mountType is the file system type of the longest /proc/self/mounts entry
// containing path.
func mountType(path string) string {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return ""
	}
	defer f.Close()
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	best, typ := -1, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		mnt := unescapeMount(fields[1])
		if (path == mnt || mnt == "/" || strings.HasPrefix(path, mnt+"/")) && len(mnt) > best {
			best, typ = len(mnt), fields[2]
		}
	}
	return typ
}

// unescapeMount decodes the octal escapes (\040 for space) of /proc/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, ok := octal3(s[i+1 : i+4]); ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func octal3(s string) (byte, bool) {
	if len(s) != 3 {
		return 0, false
	}
	var v int
	for _, c := range s {
		if c < '0' || c > '7' {
			return 0, false
		}
		v = v*8 + int(c-'0')
	}
	return byte(v), v < 256
}

// networkFS are the Linux network file systems (statfs f_type, or the FUSE
// subtype from the mount table).
var networkFS = map[string]bool{
	"nfs": true, "smb": true, "cifs": true, "smb2": true, "afs": true, "coda": true, "9p": true,
	"fuse.sshfs": true, "fuse.rclone": true, "fuse.smbnetfs": true, "fuse.curlftpfs": true,
	"fuse.davfs": true, "davfs": true, "fuse.gvfsd-fuse": true,
}
