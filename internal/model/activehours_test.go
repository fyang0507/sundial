package model

import (
	"testing"
	"time"
)

func TestParseActiveHours(t *testing.T) {
	tests := []struct {
		name       string
		spec       string
		tz         string
		wantErr    bool
		wantStart  string
		wantEnd    string
		wantTZ     string
		wantNilOut bool
	}{
		{name: "empty spec => nil", spec: "", wantNilOut: true},
		{name: "same day", spec: "08:00-22:00", wantStart: "08:00", wantEnd: "22:00"},
		{name: "overnight", spec: "22:00-02:00", wantStart: "22:00", wantEnd: "02:00"},
		{name: "zero-pads", spec: "8:5-9:0", wantStart: "08:05", wantEnd: "09:00"},
		{name: "with tz", spec: "08:00-22:00", tz: "America/Los_Angeles", wantStart: "08:00", wantEnd: "22:00", wantTZ: "America/Los_Angeles"},
		{name: "equal start/end rejected", spec: "08:00-08:00", wantErr: true},
		{name: "missing dash", spec: "0800to2200", wantErr: true},
		{name: "bad hour", spec: "24:00-08:00", wantErr: true},
		{name: "bad minute", spec: "08:60-09:00", wantErr: true},
		{name: "bad tz", spec: "08:00-22:00", tz: "Mars/Phobos", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseActiveHours(tc.spec, tc.tz)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNilOut {
				if got != nil {
					t.Fatalf("expected nil result, got %+v", got)
				}
				return
			}
			if got.Start != tc.wantStart || got.End != tc.wantEnd || got.Timezone != tc.wantTZ {
				t.Fatalf("got %+v, want start=%s end=%s tz=%s", got, tc.wantStart, tc.wantEnd, tc.wantTZ)
			}
		})
	}
}

// mustWindow builds an ActiveWindow in UTC for deterministic tests.
func mustWindow(t *testing.T, start, end string) *ActiveWindow {
	t.Helper()
	ah := &ActiveHours{Start: start, End: end, Timezone: "UTC"}
	w, err := ah.Window()
	if err != nil {
		t.Fatalf("Window(): %v", err)
	}
	return w
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

func TestActiveWindow_Contains_SameDay(t *testing.T) {
	w := mustWindow(t, "08:00", "22:00")
	cases := map[string]bool{
		"2026-07-03T07:59:00Z": false,
		"2026-07-03T08:00:00Z": true, // inclusive start
		"2026-07-03T14:30:00Z": true,
		"2026-07-03T21:59:00Z": true,
		"2026-07-03T22:00:00Z": false, // exclusive end
		"2026-07-03T23:30:00Z": false,
		"2026-07-03T03:00:00Z": false,
	}
	for ts, want := range cases {
		if got := w.Contains(at(t, ts)); got != want {
			t.Errorf("Contains(%s) = %v, want %v", ts, got, want)
		}
	}
}

func TestActiveWindow_Contains_Overnight(t *testing.T) {
	w := mustWindow(t, "22:00", "02:00")
	cases := map[string]bool{
		"2026-07-03T21:59:00Z": false,
		"2026-07-03T22:00:00Z": true, // inclusive start
		"2026-07-03T23:30:00Z": true,
		"2026-07-04T00:30:00Z": true,
		"2026-07-04T01:59:00Z": true,
		"2026-07-04T02:00:00Z": false, // exclusive end
		"2026-07-04T12:00:00Z": false,
	}
	for ts, want := range cases {
		if got := w.Contains(at(t, ts)); got != want {
			t.Errorf("Contains(%s) = %v, want %v", ts, got, want)
		}
	}
}

func TestActiveWindow_Clamp_SameDay(t *testing.T) {
	w := mustWindow(t, "08:00", "22:00")

	// Inside window: unchanged, not deferred.
	inside := at(t, "2026-07-03T09:00:00Z")
	if got, def := w.Clamp(inside); def || !got.Equal(inside) {
		t.Errorf("Clamp(inside) = (%s, %v), want (%s, false)", got, def, inside)
	}

	// Before today's open: defers to today 08:00.
	early := at(t, "2026-07-03T03:00:00Z")
	got, def := w.Clamp(early)
	if !def || !got.Equal(at(t, "2026-07-03T08:00:00Z")) {
		t.Errorf("Clamp(03:00) = (%s, %v), want (08:00 same day, true)", got, def)
	}

	// After today's close: defers to tomorrow 08:00.
	late := at(t, "2026-07-03T23:30:00Z")
	got, def = w.Clamp(late)
	if !def || !got.Equal(at(t, "2026-07-04T08:00:00Z")) {
		t.Errorf("Clamp(23:30) = (%s, %v), want (08:00 next day, true)", got, def)
	}

	// Zero time: unchanged.
	if got, def := w.Clamp(time.Time{}); def || !got.IsZero() {
		t.Errorf("Clamp(zero) = (%s, %v), want (zero, false)", got, def)
	}
}

func TestActiveWindow_Clamp_Overnight(t *testing.T) {
	w := mustWindow(t, "22:00", "02:00")

	// Midday is outside: defers to today 22:00.
	midday := at(t, "2026-07-03T12:00:00Z")
	got, def := w.Clamp(midday)
	if !def || !got.Equal(at(t, "2026-07-03T22:00:00Z")) {
		t.Errorf("Clamp(12:00) = (%s, %v), want (22:00 same day, true)", got, def)
	}

	// Just after close (02:30) defers to same-day 22:00.
	afterClose := at(t, "2026-07-03T02:30:00Z")
	got, def = w.Clamp(afterClose)
	if !def || !got.Equal(at(t, "2026-07-03T22:00:00Z")) {
		t.Errorf("Clamp(02:30) = (%s, %v), want (22:00 same day, true)", got, def)
	}

	// Inside the post-midnight tail: unchanged.
	tail := at(t, "2026-07-04T01:00:00Z")
	if got, def := w.Clamp(tail); def || !got.Equal(tail) {
		t.Errorf("Clamp(01:00) = (%s, %v), want (unchanged, false)", got, def)
	}
}

func TestActiveWindow_Describe(t *testing.T) {
	w := mustWindow(t, "08:00", "22:00")
	if got := w.Describe(); got != "08:00-22:00 UTC" {
		t.Errorf("Describe() = %q, want %q", got, "08:00-22:00 UTC")
	}
	var nilW *ActiveWindow
	if got := nilW.Describe(); got != "" {
		t.Errorf("nil Describe() = %q, want empty", got)
	}
}

func TestActiveWindow_NilSafe(t *testing.T) {
	var w *ActiveWindow
	now := at(t, "2026-07-03T03:00:00Z")
	if !w.Contains(now) {
		t.Error("nil window Contains should be true (always active)")
	}
	if got, def := w.Clamp(now); def || !got.Equal(now) {
		t.Errorf("nil window Clamp should be a no-op, got (%s, %v)", got, def)
	}
}
