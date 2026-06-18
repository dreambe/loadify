// Package version exposes build metadata, injected via -ldflags at build time.
package version

var (
	// Version is the semantic version or git tag.
	Version = "dev"
	// Commit is the git commit SHA.
	Commit = "none"
	// Date is the build timestamp.
	Date = "unknown"
)

// String returns a human-readable version string.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
