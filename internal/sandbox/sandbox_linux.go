package sandbox

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Classic BPF opcodes.
const (
	bpfLdWAbs   = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJmpJeqK  = 0x05 | 0x10
	bpfJmpJgeK  = 0x05 | 0x30
	bpfRetK     = 0x06
	retAllow    = 0x7fff_0000
	retErrno    = 0x0005_0000
	setModeFilt = 1 // SECCOMP_SET_MODE_FILTER
	flagTsync   = 1 // SECCOMP_FILTER_FLAG_TSYNC: every thread
)

func stmt(code uint16, k uint32) unix.SockFilter { return unix.SockFilter{Code: code, K: k} }
func jump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}

func auditArch() (uint32, error) {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	}
	return 0, fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
}

func denyNetwork(string) (Enforcement, error) {
	arch, err := auditArch()
	if err != nil {
		return 0, err
	}
	deny := uint32(retErrno | uint32(unix.EACCES))
	// seccomp_data: nr @0, arch @4, args[0] @16 (low 32 bits, little endian).
	prog := []unix.SockFilter{
		/* 0 */ stmt(bpfLdWAbs, 4),
		/* 1 */ jump(bpfJmpJeqK, arch, 1, 0),
		/* 2 */ stmt(bpfRetK, deny), // foreign arch: refuse
		/* 3 */ stmt(bpfLdWAbs, 0),
		/* 4 */ jump(bpfJmpJgeK, 0x4000_0000, 0, 1), // x32 ABI
		/* 5 */ stmt(bpfRetK, deny),
		/* 6 */ jump(bpfJmpJeqK, unix.SYS_IO_URING_SETUP, 0, 1),
		/* 7 */ stmt(bpfRetK, deny),
		/* 8 */ jump(bpfJmpJeqK, unix.SYS_SOCKET, 1, 0),
		/* 9 */ stmt(bpfRetK, retAllow),
		/* 10 */ stmt(bpfLdWAbs, 16),
		/* 11 */ jump(bpfJmpJeqK, unix.AF_INET, 3, 0),
		/* 12 */ jump(bpfJmpJeqK, unix.AF_INET6, 2, 0),
		/* 13 */ jump(bpfJmpJeqK, unix.AF_PACKET, 1, 0),
		/* 14 */ stmt(bpfRetK, retAllow),
		/* 15 */ stmt(bpfRetK, deny),
	}
	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return 0, fmt.Errorf("PR_SET_NO_NEW_PRIVS: %w", err)
	}
	if _, _, e := unix.Syscall(unix.SYS_SECCOMP, setModeFilt, flagTsync, uintptr(unsafe.Pointer(&fprog))); e != 0 {
		return 0, fmt.Errorf("seccomp: %w", e)
	}
	runtime.KeepAlive(prog)
	return Applied, nil
}
