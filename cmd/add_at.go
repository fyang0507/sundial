package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var addAtCmd = &cobra.Command{
	Use:   "at",
	Short: "Create a one-off schedule that fires at an absolute time",
	Long: `Create a schedule that fires exactly once at the given timestamp, then completes.

Accepted --at formats:
  - Naive local time   2026-04-20T10:00:00   interpreted in --timezone (defaults to local)
  - Zoned RFC3339      2026-04-20T10:00:00-07:00 or 2026-04-20T17:00:00Z

Past timestamps are rejected. The schedule is implicitly one-shot — no --once flag.`,
	Example: `  # Wake me tomorrow morning (local time)
  sundial add at --at "2026-04-20T10:00:00" \
    --command "say 'client call in five minutes'"

  # Pin to a specific timezone
  sundial add at --at "2026-04-20T10:00:00" --timezone "America/Los_Angeles" \
    --command "codex exec 'join the standup'"

  # Zoned timestamp (timezone flag ignored)
  sundial add at --at "2026-04-20T17:00:00Z" --command "echo hi"`,
	Run: runAddAt,
}

var (
	addAtTimestamp string
	addAtTimezone  string
)

func init() {
	addCmd.AddCommand(addAtCmd)

	addAtCmd.Flags().StringVar(&addAtTimestamp, "at", "", "ISO timestamp, e.g. 2026-04-20T10:00:00 or 2026-04-20T17:00:00Z (required)")
	addAtCmd.Flags().StringVar(&addAtTimezone, "timezone", "", "IANA timezone for naive timestamps, e.g. America/Los_Angeles (defaults to local; ignored if --at includes an offset)")

	_ = addAtCmd.MarkFlagRequired("at")
}

func runAddAt(cmd *cobra.Command, args []string) {
	validateSharedAddFlags()

	tz := addAtTimezone
	if tz == "" {
		tz = detectLocalTimezone()
	}

	fireAt, err := parseAtTimestamp(addAtTimestamp, tz)
	if err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	if !fireAt.After(time.Now()) {
		fmt.Println(format.FormatError(
			fmt.Sprintf("--at %q is not in the future (resolved to %s)", addAtTimestamp, fireAt.Format(time.RFC3339)),
			jsonOutput,
		))
		os.Exit(1)
	}

	fireAtRFC := fireAt.UTC().Format(time.RFC3339)

	cfg := model.TriggerConfig{
		Type:   model.TriggerTypeAt,
		FireAt: fireAtRFC,
		Location: &model.Location{
			Timezone: tz,
		},
	}
	params := model.AddParams{
		Type:     model.TriggerTypeAt,
		FireAt:   fireAtRFC,
		Timezone: tz,
		Once:     true, // at is implicitly one-shot; surfaces in dry-run output
	}
	applySharedAddParams(&params)

	dispatchAdd(params, cfg, tz)
}

// parseAtTimestamp accepts either a naive ISO local time (interpreted in
// defaultTZ) or a zoned RFC3339 string. Returns the resolved time.
func parseAtTimestamp(s, defaultTZ string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	loc, err := time.LoadLocation(defaultTZ)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timezone %q: %w", defaultTZ, err)
	}
	// Naive "2006-01-02T15:04:05" in the given location.
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, loc); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --at %q: expected ISO timestamp like 2026-04-20T10:00:00 or 2026-04-20T17:00:00Z", s)
}
