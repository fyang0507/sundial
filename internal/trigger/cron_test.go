package trigger

import (
	"testing"
	"time"
)

func TestCronTrigger_NextFireTime(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		after    time.Time
		expected time.Time
	}{
		{
			name:     "every minute from top of hour",
			expr:     "* * * * *",
			after:    time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC),
		},
		{
			name:     "daily at 9:00",
			expr:     "0 9 * * *",
			after:    time.Date(2025, 6, 15, 8, 0, 0, 0, time.UTC),
			expected: time.Date(2025, 6, 15, 9, 0, 0, 0, time.UTC),
		},
		{
			name:     "daily at 9:00 when after is past 9",
			expr:     "0 9 * * *",
			after:    time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC),
			expected: time.Date(2025, 6, 16, 9, 0, 0, 0, time.UTC),
		},
		{
			name:     "weekdays only at 9:00",
			expr:     "0 9 * * 1-5",
			after:    time.Date(2025, 6, 13, 10, 0, 0, 0, time.UTC), // Friday after 9
			expected: time.Date(2025, 6, 16, 9, 0, 0, 0, time.UTC), // Next Monday
		},
		{
			name:     "specific day and time",
			expr:     "30 14 * * 3",
			after:    time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC), // Sunday
			expected: time.Date(2025, 6, 18, 14, 30, 0, 0, time.UTC), // Wednesday
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger := &CronTrigger{Expr: tt.expr}
			got := trigger.NextFireTime(tt.after)
			if !got.Equal(tt.expected) {
				t.Errorf("NextFireTime(%v) = %v, want %v", tt.after, got, tt.expected)
			}
		})
	}
}

func TestCronTrigger_NextFireTime_StrictlyAfter(t *testing.T) {
	trigger := &CronTrigger{Expr: "0 9 * * *"}
	after := time.Date(2025, 6, 15, 9, 0, 0, 0, time.UTC) // exactly at 9:00
	got := trigger.NextFireTime(after)
	expected := time.Date(2025, 6, 16, 9, 0, 0, 0, time.UTC) // next day
	if !got.Equal(expected) {
		t.Errorf("NextFireTime at exact match = %v, want %v (strictly after)", got, expected)
	}
}

func TestCronTrigger_Validate(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{name: "valid every minute", expr: "* * * * *", wantErr: false},
		{name: "valid daily at 9", expr: "0 9 * * *", wantErr: false},
		{name: "valid weekdays", expr: "0 9 * * 1-5", wantErr: false},
		{name: "invalid too few fields", expr: "* * *", wantErr: true},
		{name: "invalid too many fields", expr: "* * * * * *", wantErr: true},
		{name: "invalid expression", expr: "not a cron", wantErr: true},
		{name: "empty expression", expr: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger := &CronTrigger{Expr: tt.expr}
			err := trigger.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCronTrigger_HumanDescription(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		expected string
	}{
		{
			name:     "weekday at 9:00 AM",
			expr:     "0 9 * * 1-5",
			expected: "Every weekday at 9:00 AM",
		},
		{
			name:     "daily at noon",
			expr:     "0 12 * * *",
			expected: "Every day at 12:00 PM",
		},
		{
			name:     "daily at midnight",
			expr:     "0 0 * * *",
			expected: "Every day at 12:00 AM",
		},
		{
			name:     "specific days",
			expr:     "30 14 * * 1,3,5",
			expected: "Every Mon, Wed, Fri at 2:30 PM",
		},
		{
			name:     "complex expression falls back to raw",
			expr:     "*/5 * * * *",
			expected: "cron(*/5 * * * *)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger := &CronTrigger{Expr: tt.expr}
			got := trigger.HumanDescription()
			if got != tt.expected {
				t.Errorf("HumanDescription() = %q, want %q", got, tt.expected)
			}
		})
	}
}
