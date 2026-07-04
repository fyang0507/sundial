package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Settings is the daemon-wide, runtime-mutable configuration managed by the
// daemon and persisted in local state (NOT the config file, so it can be changed
// via `sundial set-active-hours` without a restart, and NOT the data repo, since
// active hours are machine-local policy that follows the host's timezone). It is
// stored at <state>/settings.json.
type Settings struct {
	// ActiveHours is the daemon-wide firing window every schedule obeys unless it
	// opts out (DesiredState.IgnoreActiveHours). Nil means no window.
	ActiveHours *ActiveHours `json:"active_hours,omitempty"`
}

// ActiveHours restricts a schedule to fire only inside a daily local-time
// window. It is a cross-cutting suppression that applies to EVERY trigger type
// (cron, solar, poll, at) — like Precondition, it layers on top of the trigger
// rather than being part of it.
//
// The window is the half-open interval [Start, End) in the resolved timezone. A
// fire that lands outside the window is DEFERRED to the next window opening (see
// ActiveWindow.Clamp), never dropped: an occurrence at 03:00 with a 08:00-22:00
// window runs at 08:00. When Start > End the window CROSSES MIDNIGHT — e.g.
// "22:00-02:00" is active from 22:00 through 02:00 the next day.
type ActiveHours struct {
	Start string `json:"start"` // "HH:MM" local time, inclusive
	End   string `json:"end"`   // "HH:MM" local time, exclusive
	// Timezone is the IANA zone the window is interpreted in. Empty means FOLLOW
	// THE MACHINE'S CURRENT LOCAL ZONE — the window tracks whatever timezone the
	// host is in and re-evaluates when that changes (e.g. a laptop travels NYC ->
	// SFO), so "08:00-22:00" always means the operator's local morning-to-night.
	// A non-empty value PINS the window to that zone regardless of where the
	// machine is. The daemon resolves an empty zone dynamically (see
	// ActiveHours.WindowIn); ActiveHours.Window resolves it against time.Local for
	// one-shot CLI/preview use.
	Timezone string `json:"timezone,omitempty"`
}

// ParseActiveHours parses the CLI "HH:MM-HH:MM" spec (and optional timezone) into
// a validated ActiveHours. An empty spec returns (nil, nil) — no window. The
// timezone, when non-empty, must name a loadable IANA zone; empty defers to the
// daemon local zone at window-build time.
func ParseActiveHours(spec, timezone string) (*ActiveHours, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid active-hours %q: expected \"HH:MM-HH:MM\" (e.g. \"08:00-22:00\")", spec)
	}

	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])

	startMin, err := parseClock(start)
	if err != nil {
		return nil, fmt.Errorf("invalid active-hours start %q: %w", start, err)
	}
	endMin, err := parseClock(end)
	if err != nil {
		return nil, fmt.Errorf("invalid active-hours end %q: %w", end, err)
	}
	if startMin == endMin {
		return nil, fmt.Errorf("invalid active-hours %q: start and end are equal (an empty or all-day window is ambiguous)", spec)
	}

	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return nil, fmt.Errorf("invalid active-hours timezone %q: %w", timezone, err)
		}
	}

	// Normalize to zero-padded "HH:MM" so stored state is canonical regardless of
	// how the operator typed it (e.g. "8:0" -> "08:00").
	return &ActiveHours{
		Start:    formatClock(startMin),
		End:      formatClock(endMin),
		Timezone: timezone,
	}, nil
}

// Window resolves the ActiveHours against the process's time.Local for an empty
// (follow-local) zone. Suitable for one-shot CLI/preview use where time.Local is
// current; a long-lived daemon should use WindowIn with a freshly-detected zone.
func (a *ActiveHours) Window() (*ActiveWindow, error) {
	return a.WindowIn(time.Local)
}

// WindowIn resolves the ActiveHours into a runtime ActiveWindow. When Timezone
// is empty the window follows the machine local zone, so the caller passes the
// CURRENT local location (which it can re-detect over time); when Timezone is
// set the window is pinned to that IANA zone and local is ignored.
func (a *ActiveHours) WindowIn(local *time.Location) (*ActiveWindow, error) {
	if a == nil {
		return nil, nil
	}
	startMin, err := parseClock(a.Start)
	if err != nil {
		return nil, fmt.Errorf("invalid active-hours start %q: %w", a.Start, err)
	}
	endMin, err := parseClock(a.End)
	if err != nil {
		return nil, fmt.Errorf("invalid active-hours end %q: %w", a.End, err)
	}
	loc := local
	if loc == nil {
		loc = time.Local
	}
	if a.Timezone != "" {
		loc, err = time.LoadLocation(a.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid active-hours timezone %q: %w", a.Timezone, err)
		}
	}
	return &ActiveWindow{StartMin: startMin, EndMin: endMin, Loc: loc}, nil
}

// Describe returns a compact human-readable form. A follow-local window (empty
// Timezone) is suffixed "local" to signal it tracks the machine zone; a pinned
// window shows its IANA zone, e.g. "22:00-02:00 America/Los_Angeles".
func (a *ActiveHours) Describe() string {
	if a == nil {
		return ""
	}
	s := fmt.Sprintf("%s-%s", a.Start, a.End)
	if a.Timezone != "" {
		return s + " " + a.Timezone
	}
	return s + " local"
}

// ActiveWindow is the resolved, runtime form of ActiveHours: minute-of-day
// bounds in a concrete timezone. It is the object the daemon consults when
// clamping fire times.
type ActiveWindow struct {
	StartMin int            // minutes since local midnight, inclusive [0,1440)
	EndMin   int            // minutes since local midnight, exclusive [0,1440)
	Loc      *time.Location // never nil once built via ActiveHours.Window
}

// Describe renders the resolved window as "HH:MM-HH:MM <zone>", e.g.
// "08:00-22:00 America/New_York". For a follow-local window this shows the zone
// currently in effect. Returns "" for a nil window.
func (w *ActiveWindow) Describe() string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%s-%s %s", formatClock(w.StartMin), formatClock(w.EndMin), w.Loc)
}

// crossesMidnight reports whether the window wraps past midnight (Start > End),
// e.g. 22:00-02:00.
func (w *ActiveWindow) crossesMidnight() bool {
	return w.StartMin > w.EndMin
}

// Contains reports whether t falls inside the active window [Start, End). The
// comparison is done in the window's timezone. The interval is half-open: a fire
// exactly at End is outside; a fire exactly at Start is inside.
func (w *ActiveWindow) Contains(t time.Time) bool {
	if w == nil {
		return true
	}
	lt := t.In(w.Loc)
	m := lt.Hour()*60 + lt.Minute()
	if w.crossesMidnight() {
		// Active from Start through midnight and from midnight through End.
		return m >= w.StartMin || m < w.EndMin
	}
	return m >= w.StartMin && m < w.EndMin
}

// NextOpen returns the earliest instant at or after t at which the window is
// open. If t already falls inside the window it returns t unchanged. Otherwise
// it returns the next Start boundary (today's or tomorrow's, in the window's
// timezone).
func (w *ActiveWindow) NextOpen(t time.Time) time.Time {
	if w == nil {
		return t
	}
	if w.Contains(t) {
		return t
	}
	lt := t.In(w.Loc)
	// The Start boundary is always inside the window, so checking today's and
	// tomorrow's Start is sufficient to find the next opening.
	for day := 0; day <= 1; day++ {
		d := lt.AddDate(0, 0, day)
		open := time.Date(d.Year(), d.Month(), d.Day(), w.StartMin/60, w.StartMin%60, 0, 0, w.Loc)
		if !open.Before(t) {
			return open
		}
	}
	// Unreachable for a well-formed window, but degrade to t rather than panic.
	return t
}

// Clamp maps a fire time onto the active window. If the fire is inside the
// window (or the fire is the zero time, or there is no window) it is returned
// unchanged with deferred=false. Otherwise the fire is DEFERRED to the next
// window opening and deferred=true is returned so callers can record the
// suppression.
func (w *ActiveWindow) Clamp(fire time.Time) (eligible time.Time, deferred bool) {
	if w == nil || fire.IsZero() || w.Contains(fire) {
		return fire, false
	}
	return w.NextOpen(fire), true
}

// parseClock parses a "HH:MM" 24-hour clock string into minutes-since-midnight.
func parseClock(s string) (int, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("expected \"HH:MM\"")
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, fmt.Errorf("hour is not a number")
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, fmt.Errorf("minute is not a number")
	}
	if h < 0 || h > 23 {
		return 0, fmt.Errorf("hour %d out of range [0,23]", h)
	}
	if m < 0 || m > 59 {
		return 0, fmt.Errorf("minute %d out of range [0,59]", m)
	}
	return h*60 + m, nil
}

// formatClock renders minutes-since-midnight as a zero-padded "HH:MM".
func formatClock(min int) string {
	return fmt.Sprintf("%02d:%02d", min/60, min%60)
}
