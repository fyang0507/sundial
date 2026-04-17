package cmd

import (
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var addCronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Create a cron-style schedule",
	Long:  `Create a schedule that fires on a fixed cron expression.`,
	Example: `  # Daily standup, weekdays at 9am
  sundial add cron --cron "0 9 * * 1-5" \
    --command "cd ~/project && codex exec 'daily standup'"

  # Dry run — validate and preview without creating
  sundial add cron --cron "0 9 * * 1-5" --command "echo hello" --dry-run`,
	Run: runAddCron,
}

var addCronExpr string

func init() {
	addCmd.AddCommand(addCronCmd)

	addCronCmd.Flags().StringVar(&addCronExpr, "cron", "", "cron expression (required)")
	_ = addCronCmd.MarkFlagRequired("cron")
}

func runAddCron(cmd *cobra.Command, args []string) {
	validateSharedAddFlags()

	cfg := model.TriggerConfig{
		Type: model.TriggerTypeCron,
		Cron: addCronExpr,
	}
	params := model.AddParams{
		Type: model.TriggerTypeCron,
		Cron: addCronExpr,
	}
	applySharedAddParams(&params)

	dispatchAdd(params, cfg, "")
}
