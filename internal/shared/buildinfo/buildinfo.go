// Package buildinfo holds version information injected at build time.
package buildinfo

// Set via -ldflags "-X github.com/xena-studios/raptor/internal/shared/buildinfo.Version=..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable version line.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
