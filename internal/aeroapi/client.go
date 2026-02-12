package aeroapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	baseURL    = "https://aeroapi.flightaware.com/aeroapi"
	timeoutSec = 10
)

// Client is a typed HTTP client for the FlightAware AeroAPI.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new AeroAPI client with the given API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: timeoutSec * time.Second,
		},
	}
}

// doRequest performs an authenticated GET request and decodes the JSON response.
func (c *Client) doRequest(path string, params url.Values, dest any) error {
	u := baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("aeroapi: creating request: %w", err)
	}
	req.Header.Set("x-apikey", c.apiKey)
	req.Header.Set("Accept", "application/json; charset=UTF-8")

	resp, err := c.httpClient.Do(req)
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

// GetArrivals fetches recent arrivals for the given airport (e.g., "KSFO").
// It filters to airline-type flights only.
func (c *Client) GetArrivals(airportCode string) ([]Flight, error) {
	params := url.Values{
		"type":      {"Airline"},
		"max_pages": {"1"},
	}
	var resp ArrivalsResponse
	if err := c.doRequest("/airports/"+airportCode+"/flights/arrivals", params, &resp); err != nil {
		return nil, err
	}
	return resp.Arrivals, nil
}

// GetDepartures fetches recent departures for the given airport (e.g., "KSFO").
// It filters to airline-type flights only.
func (c *Client) GetDepartures(airportCode string) ([]Flight, error) {
	params := url.Values{
		"type":      {"Airline"},
		"max_pages": {"1"},
	}
	var resp DeparturesResponse
	if err := c.doRequest("/airports/"+airportCode+"/flights/departures", params, &resp); err != nil {
		return nil, err
	}
	return resp.Departures, nil
}

// GetFlightPosition fetches the latest position for a given fa_flight_id.
func (c *Client) GetFlightPosition(faFlightID string) (*InFlightPosition, error) {
	var resp InFlightPosition
	if err := c.doRequest("/flights/"+url.PathEscape(faFlightID)+"/position", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetAllFlights fetches all flights (scheduled + completed arrivals/departures) for an airport.
// This is the combined endpoint that includes en-route flights in scheduled_arrivals.
func (c *Client) GetAllFlights(airportCode string) (*AirportFlightsResponse, error) {
	params := url.Values{
		"type":      {"Airline"},
		"max_pages": {"1"},
	}
	var resp AirportFlightsResponse
	if err := c.doRequest("/airports/"+airportCode+"/flights", params, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
