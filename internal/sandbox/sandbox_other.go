//go:build !linux && !darwin

package sandbox

import "errors"

func denyNetwork(string) (Enforcement, error) {
	return 0, errors.New("unsupported platform")
}
