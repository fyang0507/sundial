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
    --trigger 'outreach reply-check --contact-id c_abc123 --channel sms' \
    --interval 2m --timeout 72h --once \
    --command "codex exec 'Reply from c_abc123. Continue campaign.'"`,
	Run: runAddPoll,
}

var (
	addPollTrigger  string
	addPollInterval string
	addPollTimeout  string
	addPollOnce     bool
)

func init() {
	addCmd.AddCommand(addPollCmd)

	addPollCmd.Flags().StringVar(&addPollTrigger, "trigger", "", "condition command; exit 0 = fire (required)")
	addPollCmd.Flags().StringVar(&addPollInterval, "interval", "", `check frequency, e.g. "2m", "5m" (required)`)
	addPollCmd.Flags().StringVar(&addPollTimeout, "timeout", "", `max lifetime, e.g. "72h", "30m" (required)`)
	addPollCmd.Flags().BoolVar(&addPollOnce, "once", false, "fire once then complete the schedule")

	for _, name := range []string{"trigger", "interval", "timeout"} {
		_ = addPollCmd.MarkFlagRequired(name)
	}
}

func runAddPoll(cmd *cobra.Command, args []string) {
	validateSharedAddFlags()

	cfg := model.TriggerConfig{
		Type:           model.TriggerTypePoll,
		TriggerCommand: addPollTrigger,
		Interval:       addPollInterval,
		Timeout:        addPollTimeout,
	}
	params := model.AddParams{
		Type:           model.TriggerTypePoll,
		TriggerCommand: addPollTrigger,
		Interval:       addPollInterval,
		Timeout:        addPollTimeout,
		Once:           addPollOnce,
	}
	applySharedAddParams(&params)

	dispatchAdd(params, cfg, "")
}
