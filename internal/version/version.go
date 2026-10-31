// Package version describes the running dockup build.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

type Info struct {
	Version  string `json:"version"`
	Commit   string `json:"commit,omitempty"`
	Date     string `json:"date,omitempty"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
}

// Resolve combines values injected via -ldflags (release builds) with the
// module/VCS info Go embeds itself (go install, plain go build). bi may be nil.
func Resolve(version, commit, date string, bi *debug.BuildInfo) Info {
	i := Info{Version: version, Commit: commit, Date: date, Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi != nil {
		if (i.Version == "" || i.Version == "dev") && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			i.Version = bi.Main.Version
		}
		s := map[string]string{}
		for _, kv := range bi.Settings {
			s[kv.Key] = kv.Value
		}
		if i.Commit == "" && s["vcs.revision"] != "" {
			i.Commit = s["vcs.revision"][:min(7, len(s["vcs.revision"]))]
			if s["vcs.modified"] == "true" {
				i.Commit += "-dirty"
			}
		}
		if i.Date == "" {
			i.Date = s["vcs.time"]
		}
	}
	switch {
	case i.Version == "":
		i.Version = "dev"
	case i.Version[0] >= '0' && i.Version[0] <= '9':
		i.Version = "v" + i.Version // goreleaser's {{.Version}} drops the v
	}
	return i
}

// Current resolves the running binary's info.
func Current(version, commit, date string) Info {
	bi, _ := debug.ReadBuildInfo()
	return Resolve(version, commit, date, bi)
}

func (i Info) String() string {
	var parts []string
	if i.Commit != "" {
		parts = append(parts, "commit "+i.Commit)
	}
	if i.Date != "" {
		parts = append(parts, "built "+i.Date)
	}
	parts = append(parts, i.Go, i.Platform)
	return fmt.Sprintf("dockup %s (%s)", i.Version, strings.Join(parts, ", "))
}
