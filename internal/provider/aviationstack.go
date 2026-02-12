package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const aviationstackBaseURL = "http://api.aviationstack.com/v1"

// AviationStackProvider implements FlightProvider using the AviationStack API.
type AviationStackProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewAviationStackProvider creates a new AviationStack provider.
func NewAviationStackProvider(apiKey string) *AviationStackProvider {
	return &AviationStackProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (a *AviationStackProvider) Name() string { return "aviationstack" }

// GetFlightsNear returns flights for the given airport.
func (a *AviationStackProvider) GetFlightsNear(airportICAO string, direction FlightDirection) ([]Flight, error) {
	params := url.Values{
		"access_key":    {a.apiKey},
		"flight_status": {"active"},
		"limit":         {"25"},
	}

	if direction == Arriving {
		params.Set("arr_icao", airportICAO)
	} else {
		params.Set("dep_icao", airportICAO)
	}

	u := aviationstackBaseURL + "/flights?" + params.Encode()
	resp, err := a.httpClient.Get(u)
	if err != nil {
		return nil, fmt.Errorf("aviationstack: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("aviationstack: HTTP %d", resp.StatusCode)
	}

	var raw asResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("aviationstack: decode error: %w", err)
	}

	var flights []Flight
	for _, f := range raw.Data {
		flight := f.toFlight()
		if flight.IsAirborne {
			flights = append(flights, flight)
		}
	}
	return flights, nil
}

// GetFlightPosition returns position for an AviationStack flight.
// AviationStack includes live data in the flight endpoint.
func (a *AviationStackProvider) GetFlightPosition(flightID string) (*FlightPosition, error) {
	params := url.Values{
		"access_key":    {a.apiKey},
		"flight_iata":   {flightID},
		"flight_status": {"active"},
	}

	u := aviationstackBaseURL + "/flights?" + params.Encode()
	resp, err := a.httpClient.Get(u)
	if err != nil {
		return nil, fmt.Errorf("aviationstack: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("aviationstack: HTTP %d", resp.StatusCode)
	}

	var raw asResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("aviationstack: decode error: %w", err)
	}

	if len(raw.Data) == 0 {
		return nil, fmt.Errorf("aviationstack: flight not found")
	}

	live := raw.Data[0].Live
	if live == nil {
		return nil, fmt.Errorf("aviationstack: no live data")
	}

	pos := &FlightPosition{
		Latitude:    live.Latitude,
		Longitude:   live.Longitude,
		Altitude:    int(live.Altitude * 3.28084 / 100),  // meters → flight levels
		Groundspeed: int(live.SpeedHorizontal * 1.94384), // m/s → knots
		Timestamp:   time.Now(),
	}

	if live.IsGround {
		pos.AltitudeChange = "-"
	} else if live.SpeedVertical > 1 {
		pos.AltitudeChange = "C"
	} else if live.SpeedVertical < -1 {
		pos.AltitudeChange = "D"
	} else {
		pos.AltitudeChange = "-"
	}

	if live.Direction > 0 {
		h := int(live.Direction)
		pos.Heading = &h
	}

	return pos, nil
}

// ── AviationStack JSON types ──

type asResponse struct {
	Data []asFlight `json:"data"`
}

type asFlight struct {
	FlightDate   string        `json:"flight_date"`
	FlightStatus string        `json:"flight_status"`
	Departure    *asAirport    `json:"departure"`
	Arrival      *asAirport    `json:"arrival"`
	Airline      *asAirline    `json:"airline"`
	Flight       *asFlightInfo `json:"flight"`
	Live         *asLive       `json:"live"`
}

type asAirport struct {
	Airport string `json:"airport"`
	IATA    string `json:"iata"`
	ICAO    string `json:"icao"`
}

type asAirline struct {
	Name string `json:"name"`
	IATA string `json:"iata"`
	ICAO string `json:"icao"`
}

type asFlightInfo struct {
	Number string `json:"number"`
	IATA   string `json:"iata"`
	ICAO   string `json:"icao"`
}

type asLive struct {
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Altitude        float64 `json:"altitude"`
	Direction       float64 `json:"direction"`
	SpeedHorizontal float64 `json:"speed_horizontal"`
	SpeedVertical   float64 `json:"speed_vertical"`
	IsGround        bool    `json:"is_ground"`
}

func (f *asFlight) toFlight() Flight {
	flight := Flight{
		IsAirborne: strings.EqualFold(f.FlightStatus, "active"),
	}

	if f.Flight != nil {
		flight.Ident = f.Flight.IATA
		flight.IdentIATA = f.Flight.IATA
		flight.IdentICAO = f.Flight.ICAO
		flight.FlightID = f.Flight.IATA // Use IATA code as ID for position lookups
	}
	if f.Airline != nil {
		flight.Operator = f.Airline.Name
		flight.OperatorIATA = f.Airline.IATA
		flight.OperatorICAO = f.Airline.ICAO
	}
	if f.Departure != nil {
		flight.Origin = &AirportRef{
			Code:     f.Departure.IATA,
			CodeICAO: f.Departure.ICAO,
			CodeIATA: f.Departure.IATA,
			Name:     f.Departure.Airport,
			City:     f.Departure.Airport,
		}
	}
	if f.Arrival != nil {
		flight.Destination = &AirportRef{
			Code:     f.Arrival.IATA,
			CodeICAO: f.Arrival.ICAO,
			CodeIATA: f.Arrival.IATA,
			Name:     f.Arrival.Airport,
			City:     f.Arrival.Airport,
		}
	}
	flight.Status = f.FlightStatus
	return flight
}
