package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/localtz"
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
	addCommand                string
	addCommandArgs            []string
	addName                   string
	addUserRequest            string
	addDryRun                 bool
	addForce                  bool
	addRefresh                bool
	addDetach                 bool
	addExecTimeout            string
	addPrecondition           string
	addPreconditionArgs       []string
	addPreconditionBackoff    string
	addPreconditionMaxElapsed string
	addIgnoreActiveHours      bool
)

func init() {
	rootCmd.AddCommand(addCmd)

	addCmd.PersistentFlags().StringVar(&addCommand, "command", "", "shell command LINE to execute (required unless --command-arg is used). Runs verbatim through `zsh -l -c` — supports pipes, redirection, globs, and variable expansion. A bare path containing a SPACE word-splits and fails with exit 127; for such a path use --command-arg instead (or quote it yourself).")
	addCmd.PersistentFlags().StringArrayVar(&addCommandArgs, "command-arg", nil, "argv-array form of the command; repeat once per argument, e.g. --command-arg /My\\ Drive/run.zsh --command-arg --flag. Each value is a distinct argv word (no shell re-parsing), so spaces never word-split and no quoting is stored. Mutually exclusive with --command; still runs under the login shell so PATH resolves.")
	addCmd.PersistentFlags().StringVar(&addName, "name", "", "human-readable schedule name")
	addCmd.PersistentFlags().StringVar(&addUserRequest, "user-request", "", "original user request that generated this schedule")
	addCmd.PersistentFlags().BoolVar(&addDryRun, "dry-run", false, "validate and preview without creating the schedule")
	addCmd.PersistentFlags().BoolVar(&addForce, "force", false, "skip duplicate detection")
	addCmd.PersistentFlags().BoolVar(&addRefresh, "refresh", false, "update existing schedule if name matches (requires --name)")
	addCmd.PersistentFlags().BoolVar(&addDetach, "detach", false, "fire-and-forget: spawn command without waiting (no exit code captured)")
	addCmd.PersistentFlags().StringVar(&addExecTimeout, "exec-timeout", "", "per-command wall-clock timeout (Go duration, e.g. \"30m\"); empty = unbounded")
	addCmd.PersistentFlags().StringVar(&addPrecondition, "precondition", "", "readiness-gate shell command LINE run before each fire; exit 0 = proceed, non-zero = defer and retry with backoff. Same space-path caveat as --command; use --precondition-arg for a script path containing spaces.")
	addCmd.PersistentFlags().StringArrayVar(&addPreconditionArgs, "precondition-arg", nil, "argv-array form of --precondition; repeat once per argument. Mutually exclusive with --precondition.")
	addCmd.PersistentFlags().StringVar(&addPreconditionBackoff, "precondition-backoff", "", "comma-separated retry backoff for a deferred --precondition (Go durations, e.g. \"1m,5m,30m,1h,2h\"); last entry repeats as cap; empty = daemon default")
	addCmd.PersistentFlags().StringVar(&addPreconditionMaxElapsed, "precondition-max-elapsed", "", "give-up budget for a deferred --precondition (Go duration); when set, terminate by elapsed budget in addition to the next-regular-fire bound")
	addCmd.PersistentFlags().BoolVar(&addIgnoreActiveHours, "ignore-active-hours", false, "exempt this schedule from the daemon-wide active-hours window (daemon.active_hours) so it fires at any hour (e.g. a 3am backup)")
}

// parsePreconditionBackoff splits the comma-separated --precondition-backoff
// flag into individual duration strings, trimming whitespace and dropping empty
// entries. It does not validate the durations — validateSharedAddFlags does.
func parsePreconditionBackoff(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// validateSharedAddFlags checks the flags that apply to every add subcommand.
func validateSharedAddFlags() {
	if addCommand == "" && len(addCommandArgs) == 0 {
		addError("--command (or --command-arg) is required")
	}
	if addCommand != "" && len(addCommandArgs) > 0 {
		addError("--command and --command-arg are mutually exclusive: use the string form (--command) OR the argv-array form (--command-arg), not both")
	}
	if addPrecondition != "" && len(addPreconditionArgs) > 0 {
		addError("--precondition and --precondition-arg are mutually exclusive")
	}
	if addRefresh && addForce {
		addError("--refresh and --force are mutually exclusive")
	}
	if addRefresh && addName == "" {
		addError("--refresh requires --name")
	}
	if addExecTimeout != "" {
		if _, err := time.ParseDuration(addExecTimeout); err != nil {
			addError(fmt.Sprintf("--exec-timeout %q is not a valid duration (e.g. \"30m\", \"90s\"): %s", addExecTimeout, err))
		}
		// A detached command's exit is never captured, so there is nothing to
		// time out — the daemon returns the moment the process is spawned.
		if addDetach {
			addError("--exec-timeout cannot be combined with --detach (a detached command has no captured exit, so a timeout has nothing to enforce)")
		}
	}
	// --precondition-backoff / --precondition-max-elapsed only have meaning when a
	// precondition is set; flag them rather than silently ignoring.
	if addPrecondition == "" && len(addPreconditionArgs) == 0 && (addPreconditionBackoff != "" || addPreconditionMaxElapsed != "") {
		addError("--precondition-backoff and --precondition-max-elapsed require --precondition (or --precondition-arg)")
	}
	for _, d := range parsePreconditionBackoff(addPreconditionBackoff) {
		if _, err := time.ParseDuration(d); err != nil {
			addError(fmt.Sprintf("--precondition-backoff entry %q is not a valid duration (e.g. \"1m\", \"30m\", \"2h\"): %s", d, err))
		}
	}
	if addPreconditionMaxElapsed != "" {
		if _, err := time.ParseDuration(addPreconditionMaxElapsed); err != nil {
			addError(fmt.Sprintf("--precondition-max-elapsed %q is not a valid duration (e.g. \"2h\", \"90m\"): %s", addPreconditionMaxElapsed, err))
		}
	}
}

// applySharedAddParams writes shared flag values into params.
func applySharedAddParams(params *model.AddParams) {
	params.Command = addCommand
	params.CommandArgs = addCommandArgs
	params.Name = addName
	params.UserRequest = addUserRequest
	params.Force = addForce
	params.Refresh = addRefresh
	params.Detach = addDetach
	params.ExecTimeout = addExecTimeout
	params.Precondition = addPrecondition
	params.PreconditionArgs = addPreconditionArgs
	params.PreconditionBackoff = parsePreconditionBackoff(addPreconditionBackoff)
	params.PreconditionMaxElapsed = addPreconditionMaxElapsed
	params.IgnoreActiveHours = addIgnoreActiveHours
}

// dispatchAdd routes to dry-run preview or daemon RPC.
// displayTimezone is used only to render times in dry-run; pass "" for non-solar triggers.
func dispatchAdd(params model.AddParams, cfg model.TriggerConfig, displayTimezone string) {
	if addDryRun {
		runAddDryRun(params, cfg, displayTimezone)
		return
	}

	client := getClient()

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
	if len(params.CommandArgs) > 0 {
		fmt.Printf("command:    %s\n", formatArgvPreview(params.CommandArgs))
	} else {
		fmt.Printf("command:    %s\n", params.Command)
	}
	if len(params.TriggerCommandArgs) > 0 {
		fmt.Printf("trigger:    %s\n", formatArgvPreview(params.TriggerCommandArgs))
	} else if params.TriggerCommand != "" {
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
	if params.ExecTimeout != "" {
		fmt.Printf("exec_timeout: %s (command killed if it runs longer)\n", params.ExecTimeout)
	}
	if len(params.PreconditionArgs) > 0 {
		fmt.Printf("precondition: %s (exit 0 = proceed; non-zero = defer and retry)\n", formatArgvPreview(params.PreconditionArgs))
		if len(params.PreconditionBackoff) > 0 {
			fmt.Printf("precondition_backoff: %s\n", strings.Join(params.PreconditionBackoff, ", "))
		}
		if params.PreconditionMaxElapsed != "" {
			fmt.Printf("precondition_max_elapsed: %s\n", params.PreconditionMaxElapsed)
		}
	} else if params.Precondition != "" {
		fmt.Printf("precondition: %s (exit 0 = proceed; non-zero = defer and retry)\n", params.Precondition)
		if len(params.PreconditionBackoff) > 0 {
			fmt.Printf("precondition_backoff: %s\n", strings.Join(params.PreconditionBackoff, ", "))
		}
		if params.PreconditionMaxElapsed != "" {
			fmt.Printf("precondition_max_elapsed: %s\n", params.PreconditionMaxElapsed)
		}
	}
	if params.IgnoreActiveHours {
		fmt.Printf("active_hours: ignored (this schedule fires at any hour, exempt from the daemon-wide window)\n")
	}
}

// detectLocalTimezone returns the system's IANA timezone name for use as the
// default display/computation zone of a solar/at schedule at add time. It reads
// the machine's current zone via the shared localtz package.
func detectLocalTimezone() string {
	return localtz.Name()
}

// formatArgvPreview renders an argv-array for the dry-run preview, quoting each
// entry so arguments with spaces read unambiguously. Display only.
func formatArgvPreview(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return "[" + strings.Join(quoted, " ") + "] (argv array)"
}

func addError(msg string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	os.Exit(1)
}
