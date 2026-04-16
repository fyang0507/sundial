package trigger

import (
	"testing"
	"time"
)

func TestPollTrigger_NextFireTime(t *testing.T) {
	trig := &PollTrigger{
		TriggerCommand: "true",
		Interval:       2 * time.Minute,
		Timeout:        time.Hour,
	}

	after := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	got := trig.NextFireTime(after)
	expected := time.Date(2025, 6, 15, 12, 2, 0, 0, time.UTC)

	if !got.Equal(expected) {
		t.Errorf("NextFireTime(%v) = %v, want %v", after, got, expected)
	}
}

func TestPollTrigger_NextFireTime_Intervals(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		after    time.Time
		expected time.Time
	}{
		{
			name:     "30 second interval",
			interval: 30 * time.Second,
			after:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2025, 1, 1, 0, 0, 30, 0, time.UTC),
		},
		{
			name:     "5 minute interval",
			interval: 5 * time.Minute,
			after:    time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2025, 1, 1, 12, 5, 0, 0, time.UTC),
		},
		{
			name:     "1 hour interval",
			interval: time.Hour,
			after:    time.Date(2025, 1, 1, 23, 30, 0, 0, time.UTC),
			expected: time.Date(2025, 1, 2, 0, 30, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trig := &PollTrigger{
				TriggerCommand: "true",
				Interval:       tt.interval,
				Timeout:        72 * time.Hour,
			}
			got := trig.NextFireTime(tt.after)
			if !got.Equal(tt.expected) {
				t.Errorf("NextFireTime(%v) = %v, want %v", tt.after, got, tt.expected)
			}
		})
	}
}

func TestPollTrigger_Validate(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		interval time.Duration
		timeout  time.Duration
		wantErr  bool
	}{
		{name: "valid", command: "check-cmd", interval: 2 * time.Minute, timeout: time.Hour, wantErr: false},
		{name: "minimum interval", command: "check-cmd", interval: 30 * time.Second, timeout: time.Minute, wantErr: false},
		{name: "timeout equals interval", command: "check-cmd", interval: 5 * time.Minute, timeout: 5 * time.Minute, wantErr: false},
		{name: "empty command", command: "", interval: 2 * time.Minute, timeout: time.Hour, wantErr: true},
		{name: "interval too short", command: "check-cmd", interval: 10 * time.Second, timeout: time.Hour, wantErr: true},
		{name: "zero interval", command: "check-cmd", interval: 0, timeout: time.Hour, wantErr: true},
		{name: "zero timeout", command: "check-cmd", interval: 2 * time.Minute, timeout: 0, wantErr: true},
		{name: "negative timeout", command: "check-cmd", interval: 2 * time.Minute, timeout: -time.Hour, wantErr: true},
		{name: "timeout less than interval", command: "check-cmd", interval: 5 * time.Minute, timeout: 2 * time.Minute, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trig := &PollTrigger{
				TriggerCommand: tt.command,
				Interval:       tt.interval,
				Timeout:        tt.timeout,
			}
			err := trig.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPollTrigger_HumanDescription(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		timeout  time.Duration
		expected string
	}{
		{
			name:     "2 minutes with 1 hour timeout",
			interval: 2 * time.Minute,
			timeout:  time.Hour,
			expected: "Poll every 2m0s (timeout 1h0m0s)",
		},
		{
			name:     "30 seconds with 5 minute timeout",
			interval: 30 * time.Second,
			timeout:  5 * time.Minute,
			expected: "Poll every 30s (timeout 5m0s)",
		},
		{
			name:     "1 hour with 72 hour timeout",
			interval: time.Hour,
			timeout:  72 * time.Hour,
			expected: "Poll every 1h0m0s (timeout 72h0m0s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trig := &PollTrigger{
				TriggerCommand: "check-cmd",
				Interval:       tt.interval,
				Timeout:        tt.timeout,
			}
			got := trig.HumanDescription()
			if got != tt.expected {
				t.Errorf("HumanDescription() = %q, want %q", got, tt.expected)
			}
		})
	}
}
