// Package buildinfo reports metadata of the running binary, never of a release
// fetched from the network. Release builds should set these strings with -X.
package buildinfo

import "runtime/debug"

// Version and Repository must describe the same build (for example v1.2.3 and
// proximavpn/proxima-vpn). An unstamped checkout remains explicitly unversioned.
var (
	Version    = "dev"
	Repository = ""
	Revision   = ""
	BuildTime  = ""
)

type Info struct {
	Version    string `json:"version"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	BuildTime  string `json:"build_time"`
	Modified   bool   `json:"modified"`
}

func Current() Info {
	i := Info{Version: Version, Repository: Repository, Revision: Revision, BuildTime: BuildTime}
	if b, ok := debug.ReadBuildInfo(); ok {
		for _, s := range b.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Revision == "" {
					i.Revision = s.Value
				}
			case "vcs.time":
				if i.BuildTime == "" {
					i.BuildTime = s.Value
				}
			case "vcs.modified":
				i.Modified = s.Value == "true"
			}
		}
	}
	return i
}
