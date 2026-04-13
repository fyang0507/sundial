package trigger

import (
	"math"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

// San Francisco coordinates for testing.
var sfLocation = model.Location{
	Lat:      37.7749,
	Lon:      -122.4194,
	Timezone: "America/Los_Angeles",
}

func TestSolarTrigger_NextFireTime_Sunrise(t *testing.T) {
	trigger := &SolarTrigger{
		Event:    model.SolarEventSunrise,
		Offset:   0,
		Days:     []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday},
		Location: sfLocation,
	}

	// June 15, 2025 — sunrise in SF is roughly around 5:47 AM PDT (12:47 UTC)
	after := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	got := trigger.NextFireTime(after)

	if got.IsZero() {
		t.Fatal("NextFireTime returned zero time")
	}

	// Sunrise should be between 12:00 and 14:00 UTC for SF in June
	if got.Hour() < 12 || got.Hour() > 14 {
		t.Errorf("Sunrise time %v seems unreasonable for SF in June (expected ~12:47 UTC)", got)
	}
}

func TestSolarTrigger_NextFireTime_Sunset(t *testing.T) {
	trigger := &SolarTrigger{
		Event:    model.SolarEventSunset,
		Offset:   0,
		Days:     []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday},
		Location: sfLocation,
	}

	// June 15, 2025 — sunset in SF is roughly around 8:35 PM PDT (03:35 UTC next day)
	after := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	got := trigger.NextFireTime(after)

	if got.IsZero() {
		t.Fatal("NextFireTime returned zero time")
	}

	// Sunset for SF in June should be between 03:00-04:30 UTC (next day, i.e., 8-9:30 PM PDT)
	gotHour := got.UTC().Hour()
	if gotHour < 2 || gotHour > 5 {
		t.Errorf("Sunset time %v (hour=%d UTC) seems unreasonable for SF in June", got, gotHour)
	}
}

func TestSolarTrigger_NextFireTime_WithOffset(t *testing.T) {
	triggerNoOffset := &SolarTrigger{
		Event:    model.SolarEventSunset,
		Offset:   0,
		Days:     []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday},
		Location: sfLocation,
	}

	triggerWithOffset := &SolarTrigger{
		Event:    model.SolarEventSunset,
		Offset:   -1 * time.Hour,
		Days:     []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday},
		Location: sfLocation,
	}

	after := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	noOffset := triggerNoOffset.NextFireTime(after)
	withOffset := triggerWithOffset.NextFireTime(after)

	diff := noOffset.Sub(withOffset)
	if math.Abs(diff.Minutes()-60) > 1 {
		t.Errorf("Expected ~60 min difference, got %v (noOffset=%v, withOffset=%v)",
			diff, noOffset, withOffset)
	}
}

func TestSolarTrigger_NextFireTime_DayFiltering(t *testing.T) {
	// June 15, 2025 is a Sunday. If we only allow Monday, next fire should be June 16.
	trigger := &SolarTrigger{
		Event:    model.SolarEventSunrise,
		Offset:   0,
		Days:     []time.Weekday{time.Monday},
		Location: sfLocation,
	}

	after := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC) // Sunday
	got := trigger.NextFireTime(after)

	if got.IsZero() {
		t.Fatal("NextFireTime returned zero time")
	}

	// Should be on Monday June 16
	gotDate := got.In(time.UTC)
	if gotDate.Month() != 6 || gotDate.Day() != 16 {
		t.Errorf("Expected fire on June 16 (Monday), got %v", got)
	}
}

func TestSolarTrigger_NextFireTime_StrictlyAfter(t *testing.T) {
	trigger := &SolarTrigger{
		Event:    model.SolarEventSunrise,
		Offset:   0,
		Days:     []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday},
		Location: sfLocation,
	}

	// Set after to exactly the expected sunrise time (approximately)
	// Use a time after today's sunrise to force it to pick the next day.
	after := time.Date(2025, 6, 15, 20, 0, 0, 0, time.UTC) // well past sunrise
	got := trigger.NextFireTime(after)

	if got.IsZero() {
		t.Fatal("NextFireTime returned zero time")
	}

	if !got.After(after) {
		t.Errorf("NextFireTime %v should be strictly after %v", got, after)
	}
}

func TestSolarTrigger_NextFireTime_366DayCap(t *testing.T) {
	// Use a trigger with no matching days — should return zero after scanning 366 days.
	trigger := &SolarTrigger{
		Event:    model.SolarEventSunrise,
		Offset:   0,
		Days:     []time.Weekday{}, // empty days — no day will match
		Location: sfLocation,
	}

	after := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	got := trigger.NextFireTime(after)

	if !got.IsZero() {
		t.Errorf("Expected zero time with empty days, got %v", got)
	}
}

func TestSolarTrigger_Validate(t *testing.T) {
	validDays := []time.Weekday{time.Monday}

	tests := []struct {
		name    string
		trigger SolarTrigger
		wantErr bool
	}{
		{
			name: "valid trigger",
			trigger: SolarTrigger{
				Event:    model.SolarEventSunrise,
				Days:     validDays,
				Location: sfLocation,
			},
			wantErr: false,
		},
		{
			name: "lat too high",
			trigger: SolarTrigger{
				Event:    model.SolarEventSunrise,
				Days:     validDays,
				Location: model.Location{Lat: 91, Lon: 0, Timezone: "UTC"},
			},
			wantErr: true,
		},
		{
			name: "lat too low",
			trigger: SolarTrigger{
				Event:    model.SolarEventSunrise,
				Days:     validDays,
				Location: model.Location{Lat: -91, Lon: 0, Timezone: "UTC"},
			},
			wantErr: true,
		},
		{
			name: "lon too high",
			trigger: SolarTrigger{
				Event:    model.SolarEventSunrise,
				Days:     validDays,
				Location: model.Location{Lat: 0, Lon: 181, Timezone: "UTC"},
			},
			wantErr: true,
		},
		{
			name: "lon too low",
			trigger: SolarTrigger{
				Event:    model.SolarEventSunrise,
				Days:     validDays,
				Location: model.Location{Lat: 0, Lon: -181, Timezone: "UTC"},
			},
			wantErr: true,
		},
		{
			name: "invalid timezone",
			trigger: SolarTrigger{
				Event:    model.SolarEventSunrise,
				Days:     validDays,
				Location: model.Location{Lat: 0, Lon: 0, Timezone: "Not/A/Timezone"},
			},
			wantErr: true,
		},
		{
			name: "invalid event",
			trigger: SolarTrigger{
				Event:    model.SolarEvent("moonrise"),
				Days:     validDays,
				Location: sfLocation,
			},
			wantErr: true,
		},
		{
			name: "empty days",
			trigger: SolarTrigger{
				Event:    model.SolarEventSunrise,
				Days:     []time.Weekday{},
				Location: sfLocation,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.trigger.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSolarTrigger_HumanDescription(t *testing.T) {
	trigger := &SolarTrigger{
		Event:  model.SolarEventSunset,
		Offset: -1 * time.Hour,
		Days:   []time.Weekday{time.Monday, time.Tuesday},
		Location: model.Location{
			Lat:      37.7749,
			Lon:      -122.4194,
			Timezone: "America/Los_Angeles",
		},
	}

	got := trigger.HumanDescription()
	expected := "Every Mon, Tue 1h before sunset (37.7749, -122.4194)"
	if got != expected {
		t.Errorf("HumanDescription() = %q, want %q", got, expected)
	}
}

func TestSolarTrigger_HumanDescription_NoOffset(t *testing.T) {
	trigger := &SolarTrigger{
		Event:  model.SolarEventSunrise,
		Offset: 0,
		Days:   []time.Weekday{time.Saturday, time.Sunday},
		Location: model.Location{
			Lat:      40.7128,
			Lon:      -74.0060,
			Timezone: "America/New_York",
		},
	}

	got := trigger.HumanDescription()
	expected := "Every Sat, Sun at sunrise (40.7128, -74.0060)"
	if got != expected {
		t.Errorf("HumanDescription() = %q, want %q", got, expected)
	}
}
