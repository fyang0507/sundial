package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/fyang0507/sundial/internal/format"
	"github.com/fyang0507/sundial/internal/geocode"
	"github.com/spf13/cobra"
)

var geocodeCmd = &cobra.Command{
	Use:   "geocode <address>",
	Short: "Look up coordinates and timezone for an address",
	Long:  `Resolve an address to latitude, longitude, and IANA timezone. Does not require the daemon.`,
	Example: `  sundial geocode "San Francisco, CA"
  sundial geocode "1600 Amphitheatre Parkway, Mountain View, CA"`,
	Args: cobra.MinimumNArgs(1),
	Run:  runGeocode,
}

func init() {
	rootCmd.AddCommand(geocodeCmd)
}

func runGeocode(cmd *cobra.Command, args []string) {
	address := strings.Join(args, " ")

	client := geocode.NewClient()
	result, err := client.Geocode(address)
	if err != nil {
		fmt.Println(format.FormatError(err.Error(), jsonOutput))
		os.Exit(1)
	}

	fmt.Println(format.FormatGeocodeResult(result.Lat, result.Lon, result.Timezone, result.DisplayName, jsonOutput))
}
