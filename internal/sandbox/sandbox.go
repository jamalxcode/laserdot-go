// Package sandbox denies the process all network access, enforced by the
// operating system (spec §7.1, pillar 1). Every laserdot process calls
// DenyNetwork first.
//
//   - Linux: a seccomp filter on every thread. Creating AF_INET, AF_INET6
//     or AF_PACKET sockets fails with EACCES; io_uring (which can open
//     sockets past seccomp) and the x32 ABI are refused outright.
//   - macOS: the process re-executes itself under /usr/bin/sandbox-exec with
//     a Seatbelt profile that allows everything except networking; Unix
//     sockets inside laserdot's socket folder (the daemon's IPC) stay
//     usable. DNS goes through a Unix socket elsewhere and is denied too.
//     (Calling sandbox_init directly needs cgo, which the port avoids;
//     Issue 002.)
//
// Either way the restriction is irreversible and inherited by every child
// process.
package sandbox

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Enforcement says how DenyNetwork succeeded.
type Enforcement int

const (
	// Applied: the restriction is now in force.
	Applied Enforcement = iota
	// AlreadySandboxed: the process was already restricted.
	AlreadySandboxed
)

func (e Enforcement) String() string {
	return [...]string{"applied", "already sandboxed"}[e]
}

// DenyNetwork denies this process (and its future children) all network
// access. On macOS, Unix sockets inside socketDir stay usable for IPC, and
// the call re-executes the program (it returns only in the restricted
// process).
func DenyNetwork(socketDir string) (Enforcement, error) {
	e, err := denyNetwork(socketDir)
	if err != nil {
		return e, fmt.Errorf("could not deny network access: %w", err)
	}
	if !IsEnforced() {
		return e, errors.New("could not deny network access: the restriction is not in force")
	}
	return e, nil
}

// IsEnforced probes whether the OS refuses this process network access:
// an IPv4 UDP socket is created and connected to the loopback discard
// port. Connecting a UDP socket sends nothing, so the probe never produces
// traffic either way.
func IsEnforced() bool {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM)
	}
	defer unix.Close(fd)
	err = unix.Connect(fd, &unix.SockaddrInet4{Port: 9, Addr: [4]byte{127, 0, 0, 1}})
	return errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM)
}

// MacOSProfile is the Seatbelt profile used on macOS (shown on the privacy
// page, §7.6).
func MacOSProfile(socketDir string) string {
	p := "(version 1)\n(allow default)\n(deny network*)\n"
	if socketDir != "" {
		dir := socketDir
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(dir)
		p += fmt.Sprintf("(allow network* (local unix-socket (subpath \"%s\")))\n"+
			"(allow network* (remote unix-socket (subpath \"%s\")))\n", q, q)
	}
	return p
}
