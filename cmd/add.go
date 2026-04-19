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
	Long: `Create a new schedule. The trigger type is selected via subcommand:

  sundial add cron    fixed cron expression
  sundial add solar   anchored to sunrise/sunset
  sundial add poll    recurring condition check
  sundial add at      one-off at an absolute timestamp`,
}

var (
	addCommand     string
	addName        string
	addUserRequest string
	addDryRun      bool
	addForce       bool
	addRefresh     bool
	addDetach      bool
)

func init() {
	rootCmd.AddCommand(addCmd)

	addCmd.PersistentFlags().StringVar(&addCommand, "command", "", "shell command to execute (required)")
	addCmd.PersistentFlags().StringVar(&addName, "name", "", "human-readable schedule name")
	addCmd.PersistentFlags().StringVar(&addUserRequest, "user-request", "", "original user request that generated this schedule")
	addCmd.PersistentFlags().BoolVar(&addDryRun, "dry-run", false, "validate and preview without creating the schedule")
	addCmd.PersistentFlags().BoolVar(&addForce, "force", false, "skip duplicate detection")
	addCmd.PersistentFlags().BoolVar(&addRefresh, "refresh", false, "update existing schedule if name matches (requires --name)")
	addCmd.PersistentFlags().BoolVar(&addDetach, "detach", false, "fire-and-forget: spawn command without waiting (no exit code captured)")
}

// validateSharedAddFlags checks the flags that apply to every add subcommand.
func validateSharedAddFlags() {
	if addCommand == "" {
		addError("--command is required")
	}
	if addRefresh && addForce {
		addError("--refresh and --force are mutually exclusive")
	}
	if addRefresh && addName == "" {
		addError("--refresh requires --name")
	}
}

// applySharedAddParams writes shared flag values into params.
func applySharedAddParams(params *model.AddParams) {
	params.Command = addCommand
	params.Name = addName
	params.UserRequest = addUserRequest
	params.Force = addForce
	params.Refresh = addRefresh
	params.Detach = addDetach
}

// dispatchAdd routes to dry-run preview or daemon RPC.
// displayTimezone is used only to render times in dry-run; pass "" for non-solar triggers.
func dispatchAdd(params model.AddParams, cfg model.TriggerConfig, displayTimezone string) {
	if addDryRun {
		runAddDryRun(params, cfg, displayTimezone)
		return
	}

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

func runAddDryRun(params model.AddParams, cfg model.TriggerConfig, displayTimezone string) {
	trig, err := trigger.ParseTrigger(cfg)
	if err != nil {
		fmt.Println(format.FormatError(fmt.Sprintf("invalid trigger: %s", err), jsonOutput))
		os.Exit(1)
	}

	next := trig.NextFireTime(time.Now())
	tz := displayTimezone
	if tz == "" {
		tz = "UTC"
	}

	fmt.Println("(dry run — no schedule created)")
	fmt.Printf("schedule:   %s\n", trig.HumanDescription())
	fmt.Printf("next_check: %s\n", format.FormatTime(next, tz))
	fmt.Printf("command:    %s\n", params.Command)
	if params.TriggerCommand != "" {
		fmt.Printf("trigger:    %s\n", params.TriggerCommand)
	}
	if params.Timeout != "" {
		fmt.Printf("timeout:    %s\n", params.Timeout)
	}
	if params.Once {
		fmt.Printf("once:       true (fires once then completes)\n")
	}
	if params.Detach {
		fmt.Printf("detach:     true (fire-and-forget; no exit code captured)\n")
	}
}

// detectLocalTimezone returns the system's IANA timezone name, falling back to
// time.Local.String() (typically "Local") when no IANA name can be resolved.
// On macOS and most Linux systems, /etc/localtime is a symlink whose target
// encodes the zone name (e.g. ".../zoneinfo/America/Los_Angeles").
func detectLocalTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		const marker = "zoneinfo/"
		if idx := strings.LastIndex(target, marker); idx >= 0 {
			return target[idx+len(marker):]
		}
	}
	return time.Local.String()
}

func addError(msg string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	os.Exit(1)
}
