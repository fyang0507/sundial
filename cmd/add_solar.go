package cmd

import (
	"fmt"
	"os"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/spf13/cobra"
)

var addSolarCmd = &cobra.Command{
	Use:   "solar",
	Short: "Create a sunrise/sunset-anchored schedule",
	Long:  `Create a schedule that fires relative to a daily solar event (sunrise or sunset).`,
	Example: `  # 1 hour before sunset on Mon/Tue
  sundial add solar --event sunset --offset "-1h" --days mon,tue \
    --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" \
    --command "cd ~/project && codex exec 'check trash bins'"

  # --timezone is optional; defaults to the machine's local timezone
  sundial add solar --event sunrise --days mon,tue,wed,thu,fri \
    --lat 37.7749 --lon -122.4194 --command "echo good morning"`,
	Run: runAddSolar,
}

var (
	addSolarEvent    string
	addSolarOffset   string
	addSolarDays     string
	addSolarLat      float64
	addSolarLon      float64
	addSolarTimezone string
	addSolarOnce     bool
)

func init() {
	addCmd.AddCommand(addSolarCmd)

	addSolarCmd.Flags().StringVar(&addSolarEvent, "event", "", "solar event: sunrise or sunset (required)")
	addSolarCmd.Flags().StringVar(&addSolarOffset, "offset", "", `offset from solar event, e.g. "-1h", "+30m"`)
	addSolarCmd.Flags().StringVar(&addSolarDays, "days", "", "comma-separated days, e.g. mon,tue,wed (required)")
	addSolarCmd.Flags().Float64Var(&addSolarLat, "lat", 0, "latitude (required)")
	addSolarCmd.Flags().Float64Var(&addSolarLon, "lon", 0, "longitude (required)")
	addSolarCmd.Flags().StringVar(&addSolarTimezone, "timezone", "", "IANA timezone, e.g. America/Los_Angeles (defaults to local timezone)")
	addSolarCmd.Flags().BoolVar(&addSolarOnce, "once", false, "fire once then complete the schedule")

	for _, name := range []string{"event", "days", "lat", "lon"} {
		_ = addSolarCmd.MarkFlagRequired(name)
	}
}

func runAddSolar(cmd *cobra.Command, args []string) {
	validateSharedAddFlags()

	tz := addSolarTimezone
	if tz == "" {
		tz = detectLocalTimezone()
	}

	days, err := model.ParseDays(addSolarDays)
	if err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	cfg := model.TriggerConfig{
		Type:  model.TriggerTypeSolar,
		Event: model.SolarEvent(addSolarEvent),
		Days:  days,
		Location: &model.Location{
			Lat:      addSolarLat,
			Lon:      addSolarLon,
			Timezone: tz,
		},
	}
	if addSolarOffset != "" {
		d, err := model.ParseOffset(addSolarOffset)
		if err != nil {
			fmt.Println(format.FormatError(err.Error(), jsonOutput))
			os.Exit(1)
		}
		cfg.Offset = model.FormatOffsetISO(d)
	}

	lat := addSolarLat
	lon := addSolarLon
	params := model.AddParams{
		Type:     model.TriggerTypeSolar,
		Event:    model.SolarEvent(addSolarEvent),
		Offset:   addSolarOffset,
		Days:     days,
		Lat:      &lat,
		Lon:      &lon,
		Timezone: tz,
		Once:     addSolarOnce,
	}
	applySharedAddParams(&params)

	dispatchAdd(params, cfg, tz)
}
