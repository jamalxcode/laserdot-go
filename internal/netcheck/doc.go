// Package netcheck enforces pillar 1 (no network, ever; spec §7.1) at build
// time, alongside the OS sandbox that enforces it at run time. Its tests fail
// if a package that speaks a network protocol enters the module's dependency
// graph, or if our own code opens anything but a Unix socket.
//
// The standard `net` package itself is allowed: the daemon's Unix socket needs
// it, and some dependencies import it without using the network (e.g.
// github.com/google/uuid lists interfaces for node ids).
package netcheck

// Banned packages: importing any of them (directly or through a dependency)
// fails the build. Prefix matches cover sub-packages.
var Banned = []string{
	"net/http",
	"net/rpc",
	"net/smtp",
	"net/mail",
	"crypto/tls",
	"golang.org/x/net",
	"google.golang.org/grpc",
	"github.com/getsentry",
	"go.opentelemetry.io",
}
