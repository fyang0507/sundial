package geocode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultNominatimURL = "https://nominatim.openstreetmap.org"
	defaultTimezoneURL  = "https://timeapi.io"
	userAgent           = "sundial/1.0 (https://github.com/fyang0507/sundial)"
)

// GeoResult holds the geocoding result for an address.
type GeoResult struct {
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Timezone    string  `json:"timezone"`
	DisplayName string  `json:"display_name"`
}

// Client performs geocoding lookups against Nominatim and timezone resolution
// against timeapi.io. Use NewClient for production defaults; tests can override
// the URL fields to point at httptest servers.
type Client struct {
	NominatimURL string
	TimezoneURL  string
	HTTPClient   *http.Client
}

// NewClient returns a Client with production defaults.
func NewClient() *Client {
	return &Client{
		NominatimURL: defaultNominatimURL,
		TimezoneURL:  defaultTimezoneURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// nominatimResult is the JSON shape returned by the Nominatim /search endpoint.
type nominatimResult struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

// timezoneResult is the JSON shape returned by timeapi.io coordinate lookup.
type timezoneResult struct {
	TimeZone string `json:"timeZone"`
}

// Geocode resolves an address string to geographic coordinates and an IANA
// timezone name. It calls Nominatim for lat/lon and timeapi.io for timezone.
// If the timezone lookup fails, the result falls back to "UTC".
func (c *Client) Geocode(address string) (*GeoResult, error) {
	// --- Step 1: Nominatim geocoding ---
	searchURL := fmt.Sprintf("%s/search?q=%s&format=json&limit=1",
		c.NominatimURL, url.QueryEscape(address))

	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building nominatim request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nominatim request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nominatim returned status %d", resp.StatusCode)
	}

	var results []nominatimResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decoding nominatim response: %w", err)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for address: %s", address)
	}

	lat, err := strconv.ParseFloat(results[0].Lat, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing latitude %q: %w", results[0].Lat, err)
	}
	lon, err := strconv.ParseFloat(results[0].Lon, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing longitude %q: %w", results[0].Lon, err)
	}

	geo := &GeoResult{
		Lat:         lat,
		Lon:         lon,
		DisplayName: results[0].DisplayName,
	}

	// --- Step 2: Timezone resolution ---
	tz, err := c.lookupTimezone(lat, lon)
	if err != nil {
		// Timezone lookup failed — fall back to UTC.
		geo.Timezone = "UTC"
		return geo, nil
	}
	geo.Timezone = tz
	return geo, nil
}

// GeocodeOffline resolves timezone for pre-known coordinates.
// Returns the IANA timezone name or an error.
func (c *Client) GeocodeOffline(lat, lon float64) (string, error) {
	return c.lookupTimezone(lat, lon)
}

// lookupTimezone calls timeapi.io to resolve coordinates to an IANA timezone
// name. It validates the returned timezone with time.LoadLocation before
// returning.
func (c *Client) lookupTimezone(lat, lon float64) (string, error) {
	tzURL := fmt.Sprintf("%s/api/timezone/coordinate?latitude=%f&longitude=%f",
		c.TimezoneURL, lat, lon)

	req, err := http.NewRequest(http.MethodGet, tzURL, nil)
	if err != nil {
		return "", fmt.Errorf("building timezone request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("timezone request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("timezone API returned status %d", resp.StatusCode)
	}

	var tzResult timezoneResult
	if err := json.NewDecoder(resp.Body).Decode(&tzResult); err != nil {
		return "", fmt.Errorf("decoding timezone response: %w", err)
	}

	if tzResult.TimeZone == "" {
		return "", fmt.Errorf("timezone API returned empty timezone")
	}

	// Validate the timezone is loadable by the Go runtime.
	if _, err := time.LoadLocation(tzResult.TimeZone); err != nil {
		return "", fmt.Errorf("invalid timezone %q from API: %w", tzResult.TimeZone, err)
	}

	return tzResult.TimeZone, nil
}
