package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var (
	setActiveHoursTZ    string
	setActiveHoursClear bool
)

var setActiveHoursCmd = &cobra.Command{
	Use:   "set-active-hours [HH:MM-HH:MM]",
	Short: "Set or clear the daemon-wide active-hours window",
	Long: `Set the daemon-wide active-hours window that every schedule obeys.

Active hours model your waking hours: a fire that lands outside the window is
deferred to the next opening (never dropped). The setting is applied to the
running daemon immediately (no restart) and persists across restarts. A schedule
can exempt itself with "sundial add --ignore-active-hours".

The window follows the machine's local timezone by default and auto-updates when
it changes (e.g. traveling NYC->SFO). Pass --tz to pin it to a fixed zone.

  sundial set-active-hours "08:00-22:00"                     # follow local zone
  sundial set-active-hours "22:00-02:00"                     # window crossing midnight
  sundial set-active-hours "08:00-22:00" --tz America/Denver # pinned zone
  sundial set-active-hours --clear                           # remove the window`,
	Run: runSetActiveHours,
}

func init() {
	rootCmd.AddCommand(setActiveHoursCmd)
	setActiveHoursCmd.Flags().StringVar(&setActiveHoursTZ, "tz", "", "pin the window to a fixed IANA timezone (e.g. \"America/Los_Angeles\"); default follows the machine's local zone")
	setActiveHoursCmd.Flags().BoolVar(&setActiveHoursClear, "clear", false, "remove the active-hours window (schedules fire at any hour)")
}

func runSetActiveHours(cmd *cobra.Command, args []string) {
	params := model.SetActiveHoursParams{Clear: setActiveHoursClear}

	if setActiveHoursClear {
		if len(args) > 0 || setActiveHoursTZ != "" {
			fmt.Fprintln(os.Stderr, "Error: --clear takes no window or --tz argument")
			os.Exit(1)
		}
	} else {
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "Error: a window \"HH:MM-HH:MM\" is required (or use --clear)")
			os.Exit(1)
		}
		params.Window = args[0]
		params.Timezone = setActiveHoursTZ
	}

	client := getClient()

	var result model.SetActiveHoursResult
	if err := client.Call(model.MethodSetActiveHours, params, &result); err != nil {
		handleCallError(err)
	}

	fmt.Println(format.FormatSetActiveHoursResult(&result, jsonOutput))
}
