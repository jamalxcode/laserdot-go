package sandbox

// Privacy gate (spec §7.1, §15.5): after DenyNetwork the process, and any
// process it starts, cannot use the network, while the IPC socket folder
// stays usable. The test re-runs its own binary as a child process (the
// restriction is irreversible).
//
// This file is the one place allowed to open TCP and UDP sockets: the
// control listener the sandboxed child tries to reach (netcheck exempts
// it by name).

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const roleEnv = "LASERDOT_SANDBOX_ROLE"

func rerun(t *testing.T, role string, port int, allowed, other string) string {
	cmd := exec.Command(os.Args[0], "-test.run=^TestSandboxedProcessCannotReachTheNetwork$", "-test.v")
	cmd.Env = append(os.Environ(), roleEnv+"="+role, fmt.Sprintf("LASERDOT_PORT=%d", port),
		"LASERDOT_ALLOWED="+allowed, "LASERDOT_OTHER="+other)
	out, _ := cmd.CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		if i := strings.Index(l, "RESULT"); i >= 0 {
			return strings.TrimSpace(l[i:])
		}
	}
	if t != nil {
		t.Fatalf("no result:\n%s", out)
	}
	return "RESULT none"
}

func tryTCP(port int, payload string) string {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return "blocked"
	}
	c.Write([]byte(payload))
	c.Close()
	return "open"
}

func tryUDP() string {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return "blocked"
	}
	defer c.Close()
	if _, err := c.WriteTo([]byte("x"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}); err != nil {
		return "blocked"
	}
	return "open"
}

func tryUnix(path string) string {
	c, err := net.Dial("unix", path)
	if err != nil {
		return "blocked"
	}
	c.Close()
	return "ok"
}

func TestSandboxedProcessCannotReachTheNetwork(t *testing.T) {
	var port int
	fmt.Sscan(os.Getenv("LASERDOT_PORT"), &port)
	allowed, other := os.Getenv("LASERDOT_ALLOWED"), os.Getenv("LASERDOT_OTHER")
	switch os.Getenv(roleEnv) {
	case "sandboxed":
		e, err := DenyNetwork(filepath.Dir(allowed))
		if err != nil || !IsEnforced() {
			fmt.Printf("RESULT deny failed: %v %v\n", e, err)
			return
		}
		// A process started after the restriction inherits it.
		child := rerun(nil, "inherited", port, allowed, other)
		fmt.Printf("RESULT tcp=%s udp=%s unix=%s unix_other=%s child[%s]\n",
			tryTCP(port, "leak\n"), tryUDP(), tryUnix(allowed), tryUnix(other), strings.TrimPrefix(child, "RESULT "))
		return
	case "inherited":
		fmt.Printf("RESULT tcp=%s udp=%s\n", tryTCP(port, "leak\n"), tryUDP())
		return
	}

	// Parent: a local TCP listener and two Unix sockets to aim at. Short
	// paths: macOS limits socket paths to 104 bytes.
	base, err := os.MkdirTemp("/tmp", "ldsb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	base, _ = filepath.EvalSymlinks(base)
	os.MkdirAll(filepath.Join(base, "ipc"), 0o700)
	os.MkdirAll(filepath.Join(base, "else"), 0o700)
	allowed, other = filepath.Join(base, "ipc", "d.sock"), filepath.Join(base, "else", "o.sock")
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	port = tcp.Addr().(*net.TCPAddr).Port
	for _, p := range []string{allowed, other} {
		l, err := net.Listen("unix", p)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
	}
	var leaked atomic.Bool
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			if strings.HasPrefix(line, "leak") {
				leaked.Store(true)
			}
			c.Close()
		}
	}()
	if tryTCP(port, "control\n") != "open" {
		t.Fatal("control connection failed — the test would prove nothing")
	}

	result := rerun(t, "sandboxed", port, allowed, other)
	t.Log(result)
	for _, want := range []string{"tcp=blocked", "udp=blocked", "unix=ok", "child[tcp=blocked udp=blocked]"} {
		if !strings.Contains(result, want) {
			t.Errorf("want %s: %s", want, result)
		}
	}
	if runtime.GOOS == "darwin" && !strings.Contains(result, "unix_other=blocked") {
		t.Errorf("only the IPC folder is allowed: %s", result)
	}
	time.Sleep(100 * time.Millisecond)
	if leaked.Load() {
		t.Error("data reached the listener")
	}
}

func TestProfileQuotesPaths(t *testing.T) {
	p := MacOSProfile(`/tmp/we"ird`)
	if !strings.Contains(p, "(deny network*)") || !strings.Contains(p, `we\"ird`) {
		t.Error(p)
	}
}
