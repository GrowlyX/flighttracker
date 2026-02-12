package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const openskyBaseURL = "https://opensky-network.org/api"

// SFO coordinates for bounding box.
const (
	sfoLatOS = 37.6213
	sfoLonOS = -122.3790
)

// OpenSkyProvider implements FlightProvider using the OpenSky Network REST API.
// Works without credentials (anonymous, 10 req/10s limit).
type OpenSkyProvider struct {
	username   string // optional, for better rate limits
	password   string // optional
	httpClient *http.Client
}

// NewOpenSkyProvider creates a new OpenSky provider. Username/password are optional.
func NewOpenSkyProvider(username, password string) *OpenSkyProvider {
	return &OpenSkyProvider{
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (o *OpenSkyProvider) Name() string { return "opensky" }

// GetFlightsNear returns airborne flights in a bounding box around the airport.
func (o *OpenSkyProvider) GetFlightsNear(airportICAO string, direction FlightDirection) ([]Flight, error) {
	// Use a ~200nm bounding box around the airport.
	lat, lon := airportCoords(airportICAO)
	delta := 3.0 // ~3 degrees ≈ 200nm
	lamin := lat - delta
	lamax := lat + delta
	lomin := lon - delta
	lomax := lon + delta

	url := fmt.Sprintf("%s/states/all?lamin=%.4f&lomin=%.4f&lamax=%.4f&lomax=%.4f",
		openskyBaseURL, lamin, lomin, lamax, lomax)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("opensky: %w", err)
	}
	req.Header.Set("User-Agent", "SFOFlightTracker/1.0")
	if o.username != "" {
		req.SetBasicAuth(o.username, o.password)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opensky: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("opensky: HTTP %d", resp.StatusCode)
	}

	var raw openskyResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("opensky: decode error: %w", err)
	}

	var flights []Flight
	for _, s := range raw.States {
		f := stateToFlight(s)
		if f.IsAirborne && f.Ident != "" {
			flights = append(flights, f)
		}
	}
	return flights, nil
}

// GetFlightPosition returns position for an OpenSky flight (callsign-based lookup).
func (o *OpenSkyProvider) GetFlightPosition(flightID string) (*FlightPosition, error) {
	// flightID is the ICAO24 hex address for OpenSky.
	url := fmt.Sprintf("%s/states/all?icao24=%s", openskyBaseURL, flightID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("opensky: %w", err)
	}
	req.Header.Set("User-Agent", "SFOFlightTracker/1.0")
	if o.username != "" {
		req.SetBasicAuth(o.username, o.password)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("opensky: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("opensky: HTTP %d", resp.StatusCode)
	}

	var raw openskyResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("opensky: decode error: %w", err)
	}

	if len(raw.States) == 0 {
		return nil, fmt.Errorf("opensky: aircraft not found")
	}

	pos := stateToPosition(raw.States[0])
	return &pos, nil
}

// ── OpenSky JSON types ──

type openskyResponse struct {
	Time   int64             `json:"time"`
	States []openskyStateVec `json:"states"`
}

// openskyStateVec is an OpenSky state vector (returned as a JSON array, not object).
// Can be 17 or 18 elements depending on whether `extended` was requested.
type openskyStateVec = []any

func stateToFlight(s openskyStateVec) Flight {
	f := Flight{IsAirborne: true}

	if len(s) > 0 {
		if icao24, ok := s[0].(string); ok {
			f.FlightID = icao24 // ICAO24 transponder hex
		}
	}
	if len(s) > 1 {
		if callsign, ok := s[1].(string); ok {
			f.Ident = trimCallsign(callsign)
			f.IdentICAO = f.Ident
		}
	}
	if len(s) > 8 && s[8] != nil {
		if onGround, ok := s[8].(bool); ok && onGround {
			f.IsAirborne = false
		}
	}
	return f
}

func stateToPosition(s openskyStateVec) FlightPosition {
	pos := FlightPosition{
		Timestamp: time.Now(),
	}

	if len(s) > 6 {
		if lat, ok := toFloat(s[6]); ok {
			pos.Latitude = lat
		}
	}
	if len(s) > 5 {
		if lon, ok := toFloat(s[5]); ok {
			pos.Longitude = lon
		}
	}
	if len(s) > 7 {
		if alt, ok := toFloat(s[7]); ok { // baro_altitude in meters
			pos.Altitude = int(alt * 3.28084 / 100) // convert to flight levels (100ft)
		}
	}
	if len(s) > 9 {
		if spd, ok := toFloat(s[9]); ok { // velocity in m/s
			pos.Groundspeed = int(spd * 1.94384) // convert to knots
		}
	}
	if len(s) > 10 {
		if hdg, ok := toFloat(s[10]); ok { // true_track in degrees
			h := int(hdg)
			pos.Heading = &h
		}
	}
	if len(s) > 11 {
		if vrate, ok := toFloat(s[11]); ok { // vertical_rate in m/s
			if vrate > 1 {
				pos.AltitudeChange = "C"
			} else if vrate < -1 {
				pos.AltitudeChange = "D"
			} else {
				pos.AltitudeChange = "-"
			}
		}
	}

	return pos
}

func toFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func trimCallsign(cs string) string {
	// OpenSky pads callsigns with spaces.
	return strings.TrimSpace(cs)
}

// airportCoords returns the lat/lon for known airports.
func airportCoords(icao string) (float64, float64) {
	coords := map[string][2]float64{
		"KSFO": {37.6213, -122.3790},
		"KLAX": {33.9425, -118.4081},
		"KJFK": {40.6413, -73.7781},
		"KORD": {41.9742, -87.9073},
		"KATL": {33.6407, -84.4277},
		"EGLL": {51.4700, -0.4543},
		"RJTT": {35.5494, 139.7798},
	}
	if c, ok := coords[icao]; ok {
		return c[0], c[1]
	}
	// Default to SFO
	return sfoLatOS, sfoLonOS
}
