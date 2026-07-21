package cmd

import (
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var addPollCmd = &cobra.Command{
	Use:   "poll",
	Short: "Create a condition-gated poll schedule",
	Long:  `Create a schedule that runs a check command on an interval; the main command fires when the check exits 0.`,
	Example: `  # Check every 2 minutes for up to 72 hours, fire once on success
  sundial add poll \
    --trigger 'your-check-command --since "$SUNDIAL_LAST_FIRED_AT"' \
    --interval 2m --timeout 72h --once \
    --command "codex exec 'condition met — continue the workflow'"`,
	Run: runAddPoll,
}

var (
	addPollTrigger     string
	addPollTriggerArgs []string
	addPollInterval    string
	addPollTimeout     string
	addPollOnce        bool
)

func init() {
	addCmd.AddCommand(addPollCmd)

	addPollCmd.Flags().StringVar(&addPollTrigger, "trigger", "", "condition command LINE; exit 0 = fire. Runs through `zsh -l -c` — a bare check-script path containing a SPACE word-splits; use --trigger-arg for that. Required unless --trigger-arg is used.")
	addPollCmd.Flags().StringArrayVar(&addPollTriggerArgs, "trigger-arg", nil, "argv-array form of --trigger; repeat once per argument. Each value is a distinct argv word (no shell re-parsing). Mutually exclusive with --trigger.")
	addPollCmd.Flags().StringVar(&addPollInterval, "interval", "", `check frequency, e.g. "2m", "5m" (required)`)
	addPollCmd.Flags().StringVar(&addPollTimeout, "timeout", "", `max lifetime, e.g. "72h", "30m" (required)`)
	addPollCmd.Flags().BoolVar(&addPollOnce, "once", false, "fire once then complete the schedule")

	for _, name := range []string{"interval", "timeout"} {
		_ = addPollCmd.MarkFlagRequired(name)
	}
}

func runAddPoll(cmd *cobra.Command, args []string) {
	validateSharedAddFlags()

	// --trigger is required, but it can be satisfied by either the string form
	// (--trigger) or the argv-array form (--trigger-arg); exactly one.
	if addPollTrigger == "" && len(addPollTriggerArgs) == 0 {
		addError("--trigger (or --trigger-arg) is required")
	}
	if addPollTrigger != "" && len(addPollTriggerArgs) > 0 {
		addError("--trigger and --trigger-arg are mutually exclusive")
	}

	cfg := model.TriggerConfig{
		Type:               model.TriggerTypePoll,
		TriggerCommand:     addPollTrigger,
		TriggerCommandArgs: addPollTriggerArgs,
		Interval:           addPollInterval,
		Timeout:            addPollTimeout,
	}
	params := model.AddParams{
		Type:               model.TriggerTypePoll,
		TriggerCommand:     addPollTrigger,
		TriggerCommandArgs: addPollTriggerArgs,
		Interval:           addPollInterval,
		Timeout:            addPollTimeout,
		Once:               addPollOnce,
	}
	applySharedAddParams(&params)

	dispatchAdd(params, cfg, "")
}
