package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"syscall"
)

// sandboxExec runs a program under a Seatbelt profile.
const sandboxExec = "/usr/bin/sandbox-exec"

// markerEnv carries the profile's hash into the re-executed process, so it
// knows it was started under that profile (and never loops).
const markerEnv = "LASERDOT_SANDBOX"

func denyNetwork(socketDir string) (Enforcement, error) {
	profile := MacOSProfile(socketDir)
	sum := sha256.Sum256([]byte(profile))
	mark := hex.EncodeToString(sum[:8])
	if v := os.Getenv(markerEnv); v != "" {
		if IsEnforced() {
			if v == mark {
				return Applied, nil
			}
			return AlreadySandboxed, nil
		}
		return 0, errors.New("started under sandbox-exec but the network is reachable")
	}
	if IsEnforced() {
		return AlreadySandboxed, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	argv := append([]string{sandboxExec, "-p", profile, exe}, os.Args[1:]...)
	env := append(os.Environ(), markerEnv+"="+mark)
	err = syscall.Exec(sandboxExec, argv, env)
	return 0, err // Exec returns only on failure
}
