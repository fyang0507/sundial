package similarity

import "testing"

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"abc", "abd", 1},
		{"abc", "abcd", 1},
		{"abc", "xyz", 3},
	}

	for _, tt := range tests {
		got := LevenshteinDistance(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNormalizedDistance(t *testing.T) {
	// Identical strings.
	if d := NormalizedDistance("abc", "abc"); d != 0 {
		t.Errorf("expected 0.0 for identical strings, got %f", d)
	}

	// Completely different strings of equal length.
	if d := NormalizedDistance("abc", "xyz"); d != 1.0 {
		t.Errorf("expected 1.0 for completely different strings, got %f", d)
	}

	// Both empty.
	if d := NormalizedDistance("", ""); d != 0 {
		t.Errorf("expected 0.0 for two empty strings, got %f", d)
	}
}

func TestIsFuzzyNameMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Exact match should return false (handled separately).
		{"my-schedule", "my-schedule", false},
		// Empty names never match.
		{"", "my-schedule", false},
		{"my-schedule", "", false},
		// Similar names (small edits).
		{"my-schedule", "my-scheduel", true},  // typo
		{"daily-backup", "daily-backups", true}, // plural
		{"trash-check", "trash-chekc", true},   // transposition
		// Different names.
		{"daily-standup", "weekly-report", false},
		{"build-project", "deploy-service", false},
		// Case insensitive.
		{"My-Schedule", "my-schedulE", false}, // exact match case-insensitive
		{"My-Schedule", "my-scheduel", true},  // fuzzy match case-insensitive
	}

	for _, tt := range tests {
		got := IsFuzzyNameMatch(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("IsFuzzyNameMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsFuzzyCommandMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Exact match should return false (handled separately).
		{"echo hello world", "echo hello world", false},
		// One is a substring of the other.
		{"cd ~/project && echo hello", "echo hello", false}, // "echo hello" is too short (< 12)
		{"cd ~/project && run-my-task", "run-my-task --flag", false}, // both < 12 won't apply, but let's check with longer
		{
			"cd ~/projects/standup && codex exec 'daily standup'",
			"codex exec 'daily standup'",
			true,
		},
		{
			"codex exec 'daily standup'",
			"cd ~/projects/standup && codex exec 'daily standup'",
			true,
		},
		// No substring relation.
		{
			"echo hello world foo bar",
			"codex exec 'daily standup'",
			false,
		},
		// Too short.
		{"echo hello", "echo hell", false},
		// Short strings below threshold.
		{"echo test", "echo test2", false},
	}

	for _, tt := range tests {
		got := IsFuzzyCommandMatch(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("IsFuzzyCommandMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
