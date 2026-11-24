package version

import "fmt"

// Build metadata injected at link time via -ldflags.
var (
	Version = "0.1.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// Info captures version metadata for CLI and diagnostics output.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Current returns the active build version metadata.
func Current() Info {
	return Info{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
}

// String renders a single-line summary of the build metadata.
func (i Info) String() string {
	return fmt.Sprintf("tagforge %s (commit: %s, built: %s)", i.Version, i.Commit, i.Date)
}
