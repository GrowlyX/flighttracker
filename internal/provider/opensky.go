package provider

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const openskyBaseURL = "https://opensky-network.org/api"
const openskyTokenURL = "https://auth.opensky-network.org/auth/realms/opensky-network/protocol/openid-connect/token"

// SFO coordinates for bounding box.
const (
	sfoLatOS = 37.6213
	sfoLonOS = -122.3790
)

// OpenSkyProvider implements FlightProvider using the OpenSky Network REST API.
// Supports OAuth2 client credentials flow (required for accounts created since mid-March 2025).
type OpenSkyProvider struct {
	clientID     string // OAuth2 client_id (env: OPENSKY_USER)
	clientSecret string // OAuth2 client_secret (env: OPENSKY_PASS)
	httpClient   *http.Client

	// OAuth2 token cache
	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

// NewOpenSkyProvider creates a new OpenSky provider.
// clientID and clientSecret are for OAuth2 client credentials flow.
// If empty, requests are made anonymously (lower rate limits).
func NewOpenSkyProvider(clientID, clientSecret string) *OpenSkyProvider {
	return &OpenSkyProvider{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (o *OpenSkyProvider) Name() string { return "opensky" }

// getToken returns a valid OAuth2 access token, fetching or refreshing as needed.
func (o *OpenSkyProvider) getToken() (string, error) {
	if o.clientID == "" {
		return "", nil // anonymous access
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	// Return cached token if still valid (with 30s buffer)
	if o.accessToken != "" && time.Now().Add(30*time.Second).Before(o.tokenExpiry) {
		return o.accessToken, nil
	}

	// Fetch new token via client credentials grant
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
	}

	resp, err := o.httpClient.Post(openskyTokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("opensky oauth: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("opensky oauth: HTTP %d from token endpoint", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // seconds
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("opensky oauth: decode error: %w", err)
	}

	o.accessToken = tokenResp.AccessToken
	o.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	log.Printf("[opensky] OAuth2 token acquired, expires in %ds", tokenResp.ExpiresIn)
	return o.accessToken, nil
}

// setAuth adds authentication to a request (Bearer token or anonymous).
func (o *OpenSkyProvider) setAuth(req *http.Request) error {
	token, err := o.getToken()
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

// GetFlightsNear returns airborne flights in a bounding box around the airport.
func (o *OpenSkyProvider) GetFlightsNear(airportICAO string, direction FlightDirection) ([]Flight, error) {
	// Use a ~60nm bounding box around the airport.
	lat, lon := airportCoords(airportICAO)
	delta := 1.0 // ~1 degree ≈ 60nm — keeps results close to the airport
	lamin := lat - delta
	lamax := lat + delta
	lomin := lon - delta
	lomax := lon + delta

	apiURL := fmt.Sprintf("%s/states/all?lamin=%.4f&lomin=%.4f&lamax=%.4f&lomax=%.4f",
		openskyBaseURL, lamin, lomin, lamax, lomax)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("opensky: %w", err)
	}
	req.Header.Set("User-Agent", "SFOFlightTracker/1.0")
	if err := o.setAuth(req); err != nil {
		return nil, fmt.Errorf("opensky: auth failed: %w", err)
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

// GetFlightPosition returns position for a flight using callsign or ICAO24 hex.
// When called cross-provider, the FlightID may not be an ICAO24 hex, so we
// fall back to searching by callsign in a bounding box around SFO.
func (o *OpenSkyProvider) GetFlightPosition(flight *Flight) (*FlightPosition, error) {
	callsign := flight.Ident
	if callsign == "" {
		callsign = flight.IdentICAO
	}

	// Try ICAO24 lookup first if FlightID looks like a hex address (6-char hex)
	if isHexAddr(flight.FlightID) {
		pos, err := o.getPositionByICAO24(flight.FlightID)
		if err == nil {
			return pos, nil
		}
		// Fall through to callsign search
	}

	// Search by callsign in a wide area around SFO
	delta := 5.0 // wider box for position polling
	apiURL := fmt.Sprintf("%s/states/all?lamin=%.4f&lomin=%.4f&lamax=%.4f&lomax=%.4f",
		openskyBaseURL, sfoLatOS-delta, sfoLonOS-delta, sfoLatOS+delta, sfoLonOS+delta)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("opensky: %w", err)
	}
	req.Header.Set("User-Agent", "SFOFlightTracker/1.0")
	if err := o.setAuth(req); err != nil {
		return nil, fmt.Errorf("opensky: auth failed: %w", err)
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

	// Find the matching callsign
	for _, s := range raw.States {
		f := stateToFlight(s)
		if strings.EqualFold(f.Ident, callsign) || strings.EqualFold(f.IdentICAO, callsign) {
			pos := stateToPosition(s)
			return &pos, nil
		}
	}

	return nil, fmt.Errorf("opensky: aircraft %q not found in area", callsign)
}

// getPositionByICAO24 looks up a single aircraft by its ICAO24 transponder hex.
func (o *OpenSkyProvider) getPositionByICAO24(icao24 string) (*FlightPosition, error) {
	apiURL := fmt.Sprintf("%s/states/all?icao24=%s", openskyBaseURL, icao24)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("opensky: %w", err)
	}
	req.Header.Set("User-Agent", "SFOFlightTracker/1.0")
	if err := o.setAuth(req); err != nil {
		return nil, fmt.Errorf("opensky: auth failed: %w", err)
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
		return nil, fmt.Errorf("opensky: ICAO24 %s not found", icao24)
	}

	pos := stateToPosition(raw.States[0])
	return &pos, nil
}

// isHexAddr returns true if the string looks like a 6-char ICAO24 hex address.
func isHexAddr(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
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

			// Extract airline ICAO prefix and flight number from callsign
			// e.g. "UAL2090" → prefix="UAL", flightNum="2090"
			if prefix, flightNum := parseCallsign(f.Ident); prefix != "" {
				f.OperatorICAO = prefix
				if iata, ok := icaoToIATACode[prefix]; ok {
					f.OperatorIATA = iata
					f.IdentIATA = iata + flightNum
				}
			}
		}
	}
	if len(s) > 8 && s[8] != nil {
		if onGround, ok := s[8].(bool); ok && onGround {
			f.IsAirborne = false
		}
	}
	return f
}

// parseCallsign splits an ICAO callsign into airline prefix and flight number.
// E.g. "UAL2090" → ("UAL", "2090"), "SWA456" → ("SWA", "456").
func parseCallsign(cs string) (prefix, flightNum string) {
	if cs == "" {
		return "", ""
	}
	// Find where letters end and digits begin
	i := 0
	for i < len(cs) && cs[i] >= 'A' && cs[i] <= 'Z' {
		i++
	}
	if i == 0 || i == len(cs) {
		return "", "" // no prefix or no flight number
	}
	return cs[:i], cs[i:]
}

// icaoToIATACode maps common ICAO airline designators to IATA codes.
var icaoToIATACode = map[string]string{
	// US Majors
	"UAL": "UA", "AAL": "AA", "DAL": "DL", "SWA": "WN", "ASA": "AS",
	"JBU": "B6", "NKS": "NK", "FFT": "F9", "HAL": "HA", "MXY": "MX",
	// US Regionals
	"SKW": "OO", "RPA": "YX", "ENY": "MQ", "PDT": "PT", "PSA": "OH",
	"JIA": "OH", "CPZ": "QX", "GJS": "G7", "ACA": "AC",
	// European
	"AFR": "AF", "BAW": "BA", "DLH": "LH", "KLM": "KL", "SAS": "SK",
	"FIN": "AY", "IBE": "IB", "AZA": "AZ", "TAP": "TP", "VIR": "VS",
	"EIN": "EI", "EZY": "U2", "RYR": "FR", "SWR": "LX", "AUA": "OS",
	"BEL": "SN", "LOT": "LO", "CSA": "OK", "EWG": "EW", "WZZ": "W6",
	"VLG": "VY", "NOZ": "DY",
	// Middle East
	"UAE": "EK", "ETD": "EY", "QTR": "QR", "THY": "TK", "SAA": "SA",
	"GFA": "GF", "MEA": "ME", "SVA": "SV", "ELY": "LY", "FDB": "FZ",
	// Asian
	"ANA": "NH", "JAL": "JL", "CPA": "CX", "SIA": "SQ", "EVA": "BR",
	"CAL": "CI", "CCA": "CA", "CSN": "CZ", "CES": "MU", "HDA": "HU",
	"KAL": "KE", "AAR": "OZ", "THA": "TH", "MAS": "MH", "VNA": "VN",
	"GIA": "GA", "AXM": "AK", "PAL": "PR", "CEB": "5J",
	"AIC": "AI", "IGO": "6E", "SEJ": "RS",
	// Oceania
	"QFA": "QF", "ANZ": "NZ", "JST": "JQ",
	// Americas
	"WJA": "WS", "AMX": "AM", "GLO": "G3", "AVA": "AV", "CMP": "CM",
	"LAN": "LA", "VOI": "Y4", "TAM": "JJ", "AZU": "AD",
	// Africa
	"ETH": "ET", "SAW": "SA", "MSR": "MS",
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
