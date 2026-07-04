// Package localtz resolves the machine's CURRENT local timezone by reading the
// OS on every call, rather than caching it the way Go's time.Local does.
//
// time.Local is populated once from $TZ / /etc/localtime when the process
// starts and never changes afterward. That is fine for short-lived CLI
// invocations, but the sundial daemon is long-lived: if the machine's timezone
// changes while it runs (e.g. the laptop travels NYC -> SFO and macOS updates
// the system zone automatically), time.Local goes stale. This package re-reads
// the source each call so the daemon can detect the change and re-evaluate
// active-hours windows against the new local zone.
package localtz

import (
	"os"
	"strings"
	"time"
)

// Name returns the system's IANA timezone name (e.g. "America/Los_Angeles"),
// preferring $TZ, then the /etc/localtime symlink target, falling back to
// time.Local.String() ("Local") when neither resolves a name. It performs only
// a getenv and a readlink, so it is cheap enough to poll frequently.
func Name() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		const marker = "zoneinfo/"
		if idx := strings.LastIndex(target, marker); idx >= 0 {
			return target[idx+len(marker):]
		}
	}
	return time.Local.String()
}

// Load resolves the current system timezone into a *time.Location, re-reading
// the OS (it does NOT cache like time.Local). It returns the resolved name and
// location; on any failure to load a named zone it degrades to time.Local so
// callers always get a usable location.
func Load() (string, *time.Location) {
	name := Name()
	if name == "" || name == "Local" {
		return name, time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return name, time.Local
	}
	return name, loc
}
