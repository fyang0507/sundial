package geocode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServers returns mock Nominatim and timezone API servers along with a
// Client wired to use them. The caller provides handler functions for each.
func newTestServers(
	nominatimHandler http.HandlerFunc,
	tzHandler http.HandlerFunc,
) (*httptest.Server, *httptest.Server, *Client) {
	nom := httptest.NewServer(nominatimHandler)
	tz := httptest.NewServer(tzHandler)
	c := NewClient()
	c.NominatimURL = nom.URL
	c.TimezoneURL = tz.URL
	return nom, tz, c
}

func TestGeocode_Success(t *testing.T) {
	nomHandler := func(w http.ResponseWriter, r *http.Request) {
		// Verify User-Agent header is set.
		if ua := r.Header.Get("User-Agent"); ua != userAgent {
			t.Errorf("expected User-Agent %q, got %q", userAgent, ua)
		}
		// Verify query parameter.
		q := r.URL.Query().Get("q")
		if q == "" {
			t.Error("expected non-empty q parameter")
		}
		results := []nominatimResult{
			{
				Lat:         "34.0522342",
				Lon:         "-118.2436849",
				DisplayName: "Los Angeles, Los Angeles County, California, United States",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}

	tzHandler := func(w http.ResponseWriter, r *http.Request) {
		resp := timezoneResult{TimeZone: "America/Los_Angeles"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}

	nom, tz, client := newTestServers(nomHandler, tzHandler)
	defer nom.Close()
	defer tz.Close()

	geo, err := client.Geocode("Los Angeles")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if geo.Lat != 34.0522342 {
		t.Errorf("expected lat 34.0522342, got %f", geo.Lat)
	}
	if geo.Lon != -118.2436849 {
		t.Errorf("expected lon -118.2436849, got %f", geo.Lon)
	}
	if geo.Timezone != "America/Los_Angeles" {
		t.Errorf("expected timezone America/Los_Angeles, got %s", geo.Timezone)
	}
	if geo.DisplayName != "Los Angeles, Los Angeles County, California, United States" {
		t.Errorf("unexpected display name: %s", geo.DisplayName)
	}
}

func TestGeocode_NoResults(t *testing.T) {
	nomHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]nominatimResult{})
	}

	tzHandler := func(w http.ResponseWriter, _ *http.Request) {
		// Should not be called.
		t.Error("timezone API should not be called when there are no geocode results")
	}

	nom, tz, client := newTestServers(nomHandler, tzHandler)
	defer nom.Close()
	defer tz.Close()

	_, err := client.Geocode("xyznonexistent12345")
	if err == nil {
		t.Fatal("expected error for no results, got nil")
	}
	expected := "no results found for address: xyznonexistent12345"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestGeocode_NominatimServerError(t *testing.T) {
	nomHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}

	tzHandler := func(w http.ResponseWriter, _ *http.Request) {
		t.Error("timezone API should not be called on nominatim failure")
	}

	nom, tz, client := newTestServers(nomHandler, tzHandler)
	defer nom.Close()
	defer tz.Close()

	_, err := client.Geocode("anywhere")
	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
	expected := "nominatim returned status 500"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestGeocode_TimezoneFallbackToUTC(t *testing.T) {
	nomHandler := func(w http.ResponseWriter, _ *http.Request) {
		results := []nominatimResult{
			{
				Lat:         "48.8566",
				Lon:         "2.3522",
				DisplayName: "Paris, Ile-de-France, Metropolitan France, France",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}

	tzHandler := func(w http.ResponseWriter, _ *http.Request) {
		// Simulate timezone API failure.
		w.WriteHeader(http.StatusInternalServerError)
	}

	nom, tz, client := newTestServers(nomHandler, tzHandler)
	defer nom.Close()
	defer tz.Close()

	geo, err := client.Geocode("Paris")
	if err != nil {
		t.Fatalf("expected no error on timezone fallback, got: %v", err)
	}

	if geo.Lat != 48.8566 {
		t.Errorf("expected lat 48.8566, got %f", geo.Lat)
	}
	if geo.Lon != 2.3522 {
		t.Errorf("expected lon 2.3522, got %f", geo.Lon)
	}
	if geo.Timezone != "UTC" {
		t.Errorf("expected timezone UTC on fallback, got %s", geo.Timezone)
	}
	if geo.DisplayName != "Paris, Ile-de-France, Metropolitan France, France" {
		t.Errorf("unexpected display name: %s", geo.DisplayName)
	}
}

func TestGeocodeOffline_Success(t *testing.T) {
	tzHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := timezoneResult{TimeZone: "Asia/Tokyo"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	tz := httptest.NewServer(tzHandler)
	defer tz.Close()

	client := NewClient()
	client.TimezoneURL = tz.URL

	tzName, err := client.GeocodeOffline(35.6762, 139.6503)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tzName != "Asia/Tokyo" {
		t.Errorf("expected Asia/Tokyo, got %s", tzName)
	}
}

func TestGeocodeOffline_Error(t *testing.T) {
	tz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer tz.Close()

	client := NewClient()
	client.TimezoneURL = tz.URL

	_, err := client.GeocodeOffline(0, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
