package trigger

import (
	"testing"

	"github.com/fyang0507/sundial/internal/model"
)

func TestParseTrigger_Cron(t *testing.T) {
	cfg := model.TriggerConfig{
		Type: model.TriggerTypeCron,
		Cron: "0 9 * * 1-5",
	}

	trigger, err := ParseTrigger(cfg)
	if err != nil {
		t.Fatalf("ParseTrigger() error = %v", err)
	}

	ct, ok := trigger.(*CronTrigger)
	if !ok {
		t.Fatalf("Expected *CronTrigger, got %T", trigger)
	}
	if ct.Expr != "0 9 * * 1-5" {
		t.Errorf("Expr = %q, want %q", ct.Expr, "0 9 * * 1-5")
	}
}

func TestParseTrigger_CronInvalid(t *testing.T) {
	cfg := model.TriggerConfig{
		Type: model.TriggerTypeCron,
		Cron: "not valid",
	}

	_, err := ParseTrigger(cfg)
	if err == nil {
		t.Fatal("Expected error for invalid cron expression")
	}
}

func TestParseTrigger_Solar(t *testing.T) {
	cfg := model.TriggerConfig{
		Type:   model.TriggerTypeSolar,
		Event:  model.SolarEventSunset,
		Offset: "-1h",
		Days:   []string{"monday", "tuesday", "wednesday"},
		Location: &model.Location{
			Lat:      37.7749,
			Lon:      -122.4194,
			Timezone: "America/Los_Angeles",
		},
	}

	trigger, err := ParseTrigger(cfg)
	if err != nil {
		t.Fatalf("ParseTrigger() error = %v", err)
	}

	st, ok := trigger.(*SolarTrigger)
	if !ok {
		t.Fatalf("Expected *SolarTrigger, got %T", trigger)
	}
	if st.Event != model.SolarEventSunset {
		t.Errorf("Event = %q, want %q", st.Event, model.SolarEventSunset)
	}
	if len(st.Days) != 3 {
		t.Errorf("len(Days) = %d, want 3", len(st.Days))
	}
}

func TestParseTrigger_SolarISO8601Offset(t *testing.T) {
	cfg := model.TriggerConfig{
		Type:   model.TriggerTypeSolar,
		Event:  model.SolarEventSunrise,
		Offset: "-PT1H30M",
		Days:   []string{"saturday"},
		Location: &model.Location{
			Lat:      40.7128,
			Lon:      -74.0060,
			Timezone: "America/New_York",
		},
	}

	trigger, err := ParseTrigger(cfg)
	if err != nil {
		t.Fatalf("ParseTrigger() error = %v", err)
	}

	st := trigger.(*SolarTrigger)
	expectedMinutes := -90.0
	if st.Offset.Minutes() != expectedMinutes {
		t.Errorf("Offset = %v (%f min), want %f min", st.Offset, st.Offset.Minutes(), expectedMinutes)
	}
}

func TestParseTrigger_SolarInvalidDay(t *testing.T) {
	cfg := model.TriggerConfig{
		Type:   model.TriggerTypeSolar,
		Event:  model.SolarEventSunrise,
		Days:   []string{"notaday"},
		Location: &model.Location{
			Lat:      0,
			Lon:      0,
			Timezone: "UTC",
		},
	}

	_, err := ParseTrigger(cfg)
	if err == nil {
		t.Fatal("Expected error for invalid day name")
	}
}

func TestParseTrigger_UnknownType(t *testing.T) {
	cfg := model.TriggerConfig{
		Type: model.TriggerType("lunar"),
	}

	_, err := ParseTrigger(cfg)
	if err == nil {
		t.Fatal("Expected error for unknown trigger type")
	}
}

func TestParseTrigger_SolarNoOffset(t *testing.T) {
	cfg := model.TriggerConfig{
		Type:  model.TriggerTypeSolar,
		Event: model.SolarEventSunrise,
		Days:  []string{"sunday"},
		Location: &model.Location{
			Lat:      51.5074,
			Lon:      -0.1278,
			Timezone: "Europe/London",
		},
	}

	trigger, err := ParseTrigger(cfg)
	if err != nil {
		t.Fatalf("ParseTrigger() error = %v", err)
	}

	st := trigger.(*SolarTrigger)
	if st.Offset != 0 {
		t.Errorf("Offset = %v, want 0", st.Offset)
	}
}
