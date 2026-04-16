package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/trigger"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Create a new schedule",
	Long:  `Create a new cron, solar, or poll schedule. The schedule is sent to the daemon and persisted to the data repo.`,
	Example: `  # Static cron schedule
  sundial add --type cron --cron "0 9 * * 1-5" \
    --command "cd ~/project && codex exec 'daily standup'"

  # Solar schedule
  sundial add --type solar --event sunset --offset "-1h" --days mon,tue \
    --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" \
    --command "cd ~/project && codex exec 'check trash bins'"

  # Poll trigger — check condition every 2 minutes, fire once when met
  sundial add --type poll \
    --trigger 'outreach reply-check --contact-id c_abc123 --channel sms' \
    --interval 2m --once \
    --command "codex exec 'Reply from c_abc123. Continue campaign.'"

  # Dry run — validate and preview without creating
  sundial add --type cron --cron "0 9 * * 1-5" \
    --command "echo hello" --dry-run`,
	Run: runAdd,
}

var (
	addType           string
	addCron           string
	addEvent          string
	addOffset         string
	addDays           string
	addLat            float64
	addLon            float64
	addTimezone       string
	addTriggerCommand string
	addInterval       string
	addCommand        string
	addName           string
	addUserRequest    string
	addDryRun         bool
	addForce          bool
	addOnce           bool
	addLatSet         bool
	addLonSet         bool
)

func init() {
	rootCmd.AddCommand(addCmd)

	addCmd.Flags().StringVar(&addType, "type", "", "trigger type: cron, solar, or poll (required)")
	addCmd.Flags().StringVar(&addCron, "cron", "", "cron expression (required for --type cron)")
	addCmd.Flags().StringVar(&addEvent, "event", "", "solar event: sunrise or sunset (required for --type solar)")
	addCmd.Flags().StringVar(&addOffset, "offset", "", "offset from solar event, e.g. \"-1h\", \"+30m\"")
	addCmd.Flags().StringVar(&addDays, "days", "", "comma-separated days, e.g. mon,tue,wed (required for --type solar)")
	addCmd.Flags().Float64Var(&addLat, "lat", 0, "latitude (required for --type solar)")
	addCmd.Flags().Float64Var(&addLon, "lon", 0, "longitude (required for --type solar)")
	addCmd.Flags().StringVar(&addTimezone, "timezone", "", "IANA timezone, e.g. America/Los_Angeles (required for --type solar)")
	addCmd.Flags().StringVar(&addTriggerCommand, "trigger", "", "condition command; exit 0 = fire (required for --type poll)")
	addCmd.Flags().StringVar(&addInterval, "interval", "", "check frequency, e.g. \"2m\", \"5m\" (required for --type poll)")
	addCmd.Flags().StringVar(&addCommand, "command", "", "shell command to execute (required)")
	addCmd.Flags().StringVar(&addName, "name", "", "human-readable schedule name")
	addCmd.Flags().StringVar(&addUserRequest, "user-request", "", "original user request that generated this schedule")
	addCmd.Flags().BoolVar(&addDryRun, "dry-run", false, "validate and preview without creating the schedule")
	addCmd.Flags().BoolVar(&addForce, "force", false, "skip duplicate detection")
	addCmd.Flags().BoolVar(&addOnce, "once", false, "fire once then complete the schedule")
}

func runAdd(cmd *cobra.Command, args []string) {
	// Track whether lat/lon were explicitly set.
	addLatSet = cmd.Flags().Changed("lat")
	addLonSet = cmd.Flags().Changed("lon")

	// Validate required flags.
	if addCommand == "" {
		addError("--command is required")
	}
	if addType == "" {
		addError("--type is required (cron or solar)")
	}

	switch model.TriggerType(addType) {
	case model.TriggerTypeCron:
		if addCron == "" {
			addError("--cron is required for --type cron\n\n  Example: sundial add --type cron --cron \"0 9 * * 1-5\" --command \"echo hello\"")
		}
	case model.TriggerTypeSolar:
		var missing []string
		if addEvent == "" {
			missing = append(missing, "--event")
		}
		if addDays == "" {
			missing = append(missing, "--days")
		}
		if !addLatSet {
			missing = append(missing, "--lat")
		}
		if !addLonSet {
			missing = append(missing, "--lon")
		}
		if addTimezone == "" {
			missing = append(missing, "--timezone")
		}
		if len(missing) > 0 {
			addError(fmt.Sprintf("%s required for --type solar\n\n  Example: sundial add --type solar --event sunset --offset \"-1h\" --days mon,tue \\\n    --lat 37.7749 --lon -122.4194 --timezone \"America/Los_Angeles\" \\\n    --command \"echo hello\"",
				strings.Join(missing, ", ")))
		}
	case model.TriggerTypePoll:
		var missing []string
		if addTriggerCommand == "" {
			missing = append(missing, "--trigger")
		}
		if addInterval == "" {
			missing = append(missing, "--interval")
		}
		if len(missing) > 0 {
			addError(fmt.Sprintf("%s required for --type poll\n\n  Example: sundial add --type poll --trigger 'check-cmd' --interval 2m \\\n    --command \"echo hello\" --once",
				strings.Join(missing, ", ")))
		}
	default:
		addError(fmt.Sprintf("invalid --type %q: must be cron, solar, or poll", addType))
	}

	// Dry-run mode: validate locally and preview.
	if addDryRun {
		runAddDryRun()
		return
	}

	// Normal mode: send to daemon via RPC.
	params := buildAddParams()

	client, err := getClient()
	if err != nil {
		handleClientError(err)
	}

	var result model.AddResult
	if err := client.Call(model.MethodAdd, params, &result); err != nil {
		handleCallError(err)
	}

	fmt.Println(format.FormatAddResult(&result, jsonOutput))
}

func runAddDryRun() {
	trigCfg := buildTriggerConfig()

	trig, err := trigger.ParseTrigger(trigCfg)
	if err != nil {
		fmt.Println(format.FormatError(fmt.Sprintf("invalid trigger: %s", err), jsonOutput))
		os.Exit(1)
	}

	next := trig.NextFireTime(time.Now())
	tz := addTimezone
	if tz == "" {
		tz = "UTC"
	}

	fmt.Println("(dry run — no schedule created)")
	fmt.Printf("schedule:   %s\n", trig.HumanDescription())
	fmt.Printf("next_check: %s\n", format.FormatTime(next, tz))
	fmt.Printf("command:    %s\n", addCommand)
	if addTriggerCommand != "" {
		fmt.Printf("trigger:    %s\n", addTriggerCommand)
	}
	if addOnce {
		fmt.Printf("once:       true (fires once then completes)\n")
	}
}

func buildTriggerConfig() model.TriggerConfig {
	cfg := model.TriggerConfig{
		Type: model.TriggerType(addType),
	}

	switch cfg.Type {
	case model.TriggerTypeCron:
		cfg.Cron = addCron
	case model.TriggerTypeSolar:
		cfg.Event = model.SolarEvent(addEvent)
		if addOffset != "" {
			d, err := model.ParseOffset(addOffset)
			if err != nil {
				fmt.Println(format.FormatError(err.Error(), jsonOutput))
				os.Exit(1)
			}
			cfg.Offset = model.FormatOffsetISO(d)
		}
		if addDays != "" {
			days, err := model.ParseDays(addDays)
			if err != nil {
				fmt.Println(format.FormatError(err.Error(), jsonOutput))
				os.Exit(1)
			}
			cfg.Days = days
		}
		cfg.Location = &model.Location{
			Lat:      addLat,
			Lon:      addLon,
			Timezone: addTimezone,
		}
	case model.TriggerTypePoll:
		cfg.TriggerCommand = addTriggerCommand
		cfg.Interval = addInterval
	}

	return cfg
}

func buildAddParams() model.AddParams {
	params := model.AddParams{
		Type:        model.TriggerType(addType),
		Command:     addCommand,
		Name:        addName,
		UserRequest: addUserRequest,
		Force:       addForce,
		Once:        addOnce,
	}

	switch params.Type {
	case model.TriggerTypeCron:
		params.Cron = addCron
	case model.TriggerTypeSolar:
		params.Event = model.SolarEvent(addEvent)
		params.Offset = addOffset
		if addDays != "" {
			days, err := model.ParseDays(addDays)
			if err != nil {
				fmt.Println(format.FormatError(err.Error(), jsonOutput))
				os.Exit(1)
			}
			params.Days = days
		}
		lat := addLat
		lon := addLon
		params.Lat = &lat
		params.Lon = &lon
		params.Timezone = addTimezone
	case model.TriggerTypePoll:
		params.TriggerCommand = addTriggerCommand
		params.Interval = addInterval
	}

	return params
}

func addError(msg string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	os.Exit(1)
}
