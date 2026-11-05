// Package version exposes the build identity so the UI can tell which image is
// actually running (the usual confusion when a pull silently did not happen).
package version

// Commit is injected at build time:
//
//	go build -ldflags "-X easy_proxies/internal/version.Commit=<sha>"
var Commit = "dev"

// Short returns a compact form safe for a badge.
func Short() string {
	if len(Commit) > 7 {
		return Commit[:7]
	}
	return Commit
}
