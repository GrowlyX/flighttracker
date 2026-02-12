package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const aeroAPIBaseURL = "https://aeroapi.flightaware.com/aeroapi"

// AeroAPIProvider implements FlightProvider using FlightAware AeroAPI.
type AeroAPIProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewAeroAPIProvider creates a new AeroAPI provider.
func NewAeroAPIProvider(apiKey string) *AeroAPIProvider {
	return &AeroAPIProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (a *AeroAPIProvider) Name() string { return "aeroapi" }

func (a *AeroAPIProvider) doRequest(path string, params url.Values, dest any) error {
	u := aeroAPIBaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("aeroapi: creating request: %w", err)
	}
	req.Header.Set("x-apikey", a.apiKey)
	req.Header.Set("Accept", "application/json; charset=UTF-8")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("aeroapi: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("aeroapi: HTTP %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("aeroapi: decoding response: %w", err)
	}
	return nil
}

// GetFlightsNear returns en-route flights near the given airport.
func (a *AeroAPIProvider) GetFlightsNear(airportICAO string, direction FlightDirection) ([]Flight, error) {
	params := url.Values{
		"type":      {"Airline"},
		"max_pages": {"1"},
	}

	var endpoint string
	if direction == Arriving {
		endpoint = "/airports/" + airportICAO + "/flights/arrivals"
	} else {
		endpoint = "/airports/" + airportICAO + "/flights/departures"
	}

	var raw struct {
		Arrivals   []aeroFlight `json:"arrivals"`
		Departures []aeroFlight `json:"departures"`
	}
	if err := a.doRequest(endpoint, params, &raw); err != nil {
		return nil, err
	}

	var rawFlights []aeroFlight
	if direction == Arriving {
		rawFlights = raw.Arrivals
	} else {
		rawFlights = raw.Departures
	}

	var result []Flight
	for _, f := range rawFlights {
		if f.ActualOff != nil && f.ActualOn == nil && !f.Cancelled {
			result = append(result, f.toFlight())
		}
	}
	return result, nil
}

// GetFlightPosition returns the latest position for a flight.
func (a *AeroAPIProvider) GetFlightPosition(flight *Flight) (*FlightPosition, error) {
	var raw struct {
		LastPosition *aeroPosition `json:"last_position"`
		ActualOn     *time.Time    `json:"actual_on"`
	}
	if err := a.doRequest("/flights/"+url.PathEscape(flight.FlightID)+"/position", nil, &raw); err != nil {
		return nil, err
	}
	if raw.LastPosition == nil {
		return nil, fmt.Errorf("aeroapi: no position data")
	}
	pos := raw.LastPosition.toPosition()
	return &pos, nil
}

// ── AeroAPI JSON types ──

type aeroAirportRef struct {
	Code     *string `json:"code"`
	CodeICAO *string `json:"code_icao"`
	CodeIATA *string `json:"code_iata"`
	Name     *string `json:"name"`
	City     *string `json:"city"`
}

func (a *aeroAirportRef) toAirportRef() *AirportRef {
	if a == nil {
		return nil
	}
	ref := &AirportRef{}
	if a.Code != nil {
		ref.Code = *a.Code
	}
	if a.CodeICAO != nil {
		ref.CodeICAO = *a.CodeICAO
	}
	if a.CodeIATA != nil {
		ref.CodeIATA = *a.CodeIATA
	}
	if a.Name != nil {
		ref.Name = *a.Name
	}
	if a.City != nil {
		ref.City = *a.City
	}
	return ref
}

type aeroFlight struct {
	Ident        string          `json:"ident"`
	IdentICAO    *string         `json:"ident_icao"`
	IdentIATA    *string         `json:"ident_iata"`
	FAFlightID   string          `json:"fa_flight_id"`
	Operator     *string         `json:"operator"`
	OperatorICAO *string         `json:"operator_icao"`
	OperatorIATA *string         `json:"operator_iata"`
	FlightNumber *string         `json:"flight_number"`
	Origin       *aeroAirportRef `json:"origin"`
	Destination  *aeroAirportRef `json:"destination"`
	Status       string          `json:"status"`
	AircraftType *string         `json:"aircraft_type"`
	FlightType   string          `json:"type"`
	Cancelled    bool            `json:"cancelled"`
	ActualOff    *time.Time      `json:"actual_off"`
	ActualOn     *time.Time      `json:"actual_on"`
}

func (f *aeroFlight) toFlight() Flight {
	flight := Flight{
		Ident:       f.Ident,
		FlightID:    f.FAFlightID,
		Status:      f.Status,
		IsAirborne:  f.ActualOff != nil && f.ActualOn == nil,
		Origin:      f.Origin.toAirportRef(),
		Destination: f.Destination.toAirportRef(),
	}
	if f.IdentICAO != nil {
		flight.IdentICAO = *f.IdentICAO
	}
	if f.IdentIATA != nil {
		flight.IdentIATA = *f.IdentIATA
	}
	if f.Operator != nil {
		flight.Operator = *f.Operator
	}
	if f.OperatorICAO != nil {
		flight.OperatorICAO = *f.OperatorICAO
	}
	if f.OperatorIATA != nil {
		flight.OperatorIATA = *f.OperatorIATA
	}
	if f.FlightNumber != nil {
		flight.FlightNumber = *f.FlightNumber
	}
	if f.AircraftType != nil {
		flight.AircraftType = *f.AircraftType
	}
	return flight
}

type aeroPosition struct {
	Altitude       int       `json:"altitude"`
	AltitudeChange string    `json:"altitude_change"`
	Groundspeed    int       `json:"groundspeed"`
	Heading        *int      `json:"heading"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	Timestamp      time.Time `json:"timestamp"`
}

func (p *aeroPosition) toPosition() FlightPosition {
	return FlightPosition{
		Altitude:       p.Altitude,
		AltitudeChange: p.AltitudeChange,
		Groundspeed:    p.Groundspeed,
		Heading:        p.Heading,
		Latitude:       p.Latitude,
		Longitude:      p.Longitude,
		Timestamp:      p.Timestamp,
	}
}
