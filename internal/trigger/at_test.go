package trigger

import (
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/model"
)

func TestAtTrigger_NextFireTime_Future(t *testing.T) {
	fireAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	trig := &AtTrigger{FireAt: fireAt}

	after := time.Date(2026, 4, 19, 9, 0, 0, 0, time.UTC)
	got := trig.NextFireTime(after)
	if !got.Equal(fireAt) {
		t.Errorf("NextFireTime(%v) = %v, want %v", after, got, fireAt)
	}
}

func TestAtTrigger_NextFireTime_AtOrPast(t *testing.T) {
	fireAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	trig := &AtTrigger{FireAt: fireAt}

	tests := []struct {
		name  string
		after time.Time
	}{
		{name: "exactly at fire time", after: fireAt},
		{name: "one second past", after: fireAt.Add(time.Second)},
		{name: "one day past", after: fireAt.Add(24 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trig.NextFireTime(tt.after)
			if !got.IsZero() {
				t.Errorf("NextFireTime(%v) = %v, want zero", tt.after, got)
			}
		})
	}
}

func TestAtTrigger_Validate(t *testing.T) {
	tests := []struct {
		name    string
		fireAt  time.Time
		wantErr bool
	}{
		{name: "valid", fireAt: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), wantErr: false},
		{name: "zero time", fireAt: time.Time{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trig := &AtTrigger{FireAt: tt.fireAt}
			err := trig.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAtTrigger_HumanDescription(t *testing.T) {
	fireAt := time.Date(2026, 4, 20, 17, 0, 0, 0, time.UTC)

	t.Run("UTC default", func(t *testing.T) {
		trig := &AtTrigger{FireAt: fireAt}
		got := trig.HumanDescription()
		want := "At Mon Apr 20 5:00 PM UTC"
		if got != want {
			t.Errorf("HumanDescription() = %q, want %q", got, want)
		}
	})

	t.Run("with display timezone", func(t *testing.T) {
		trig := &AtTrigger{FireAt: fireAt, DisplayTimezone: "America/Los_Angeles"}
		got := trig.HumanDescription()
		// 2026-04-20 17:00 UTC → 10:00 AM PDT.
		want := "At Mon Apr 20 10:00 AM PDT"
		if got != want {
			t.Errorf("HumanDescription() = %q, want %q", got, want)
		}
	})
}

func TestParseAtTrigger(t *testing.T) {
	t.Run("valid RFC3339", func(t *testing.T) {
		cfg := model.TriggerConfig{
			Type:   model.TriggerTypeAt,
			FireAt: "2026-04-20T10:00:00Z",
		}
		trig, err := ParseTrigger(cfg)
		if err != nil {
			t.Fatalf("ParseTrigger() error = %v", err)
		}
		at, ok := trig.(*AtTrigger)
		if !ok {
			t.Fatalf("expected *AtTrigger, got %T", trig)
		}
		expected := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
		if !at.FireAt.Equal(expected) {
			t.Errorf("FireAt = %v, want %v", at.FireAt, expected)
		}
	})

	t.Run("zoned offset normalized to UTC", func(t *testing.T) {
		cfg := model.TriggerConfig{
			Type:   model.TriggerTypeAt,
			FireAt: "2026-04-20T10:00:00-07:00",
		}
		trig, err := ParseTrigger(cfg)
		if err != nil {
			t.Fatalf("ParseTrigger() error = %v", err)
		}
		at := trig.(*AtTrigger)
		expected := time.Date(2026, 4, 20, 17, 0, 0, 0, time.UTC)
		if !at.FireAt.Equal(expected) {
			t.Errorf("FireAt = %v, want %v", at.FireAt, expected)
		}
	})

	t.Run("display timezone from location", func(t *testing.T) {
		cfg := model.TriggerConfig{
			Type:     model.TriggerTypeAt,
			FireAt:   "2026-04-20T10:00:00Z",
			Location: &model.Location{Timezone: "America/Los_Angeles"},
		}
		trig, err := ParseTrigger(cfg)
		if err != nil {
			t.Fatalf("ParseTrigger() error = %v", err)
		}
		at := trig.(*AtTrigger)
		if at.DisplayTimezone != "America/Los_Angeles" {
			t.Errorf("DisplayTimezone = %q, want %q", at.DisplayTimezone, "America/Los_Angeles")
		}
	})

	t.Run("empty fire_at", func(t *testing.T) {
		cfg := model.TriggerConfig{Type: model.TriggerTypeAt}
		if _, err := ParseTrigger(cfg); err == nil {
			t.Error("ParseTrigger() expected error, got nil")
		}
	})

	t.Run("invalid timestamp", func(t *testing.T) {
		cfg := model.TriggerConfig{
			Type:   model.TriggerTypeAt,
			FireAt: "not a timestamp",
		}
		if _, err := ParseTrigger(cfg); err == nil {
			t.Error("ParseTrigger() expected error, got nil")
		}
	})
}
