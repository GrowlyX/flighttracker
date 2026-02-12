package aeroapi

import "time"

// AirportRef represents a reference to an airport in flight data.
type AirportRef struct {
	Code           *string `json:"code"`
	CodeICAO       *string `json:"code_icao"`
	CodeIATA       *string `json:"code_iata"`
	CodeLID        *string `json:"code_lid"`
	Timezone       *string `json:"timezone"`
	Name           *string `json:"name"`
	City           *string `json:"city"`
	AirportInfoURL *string `json:"airport_info_url"`
}

// DisplayCode returns the best available airport code for display.
func (a *AirportRef) DisplayCode() string {
	if a == nil {
		return "???"
	}
	if a.CodeIATA != nil && *a.CodeIATA != "" {
		return *a.CodeIATA
	}
	if a.CodeICAO != nil && *a.CodeICAO != "" {
		return *a.CodeICAO
	}
	if a.Code != nil && *a.Code != "" {
		return *a.Code
	}
	return "???"
}

// DisplayName returns the best available airport name for display.
func (a *AirportRef) DisplayName() string {
	if a == nil {
		return "Unknown"
	}
	if a.Name != nil && *a.Name != "" {
		return *a.Name
	}
	if a.City != nil && *a.City != "" {
		return *a.City
	}
	return a.DisplayCode()
}

// DisplayCity returns the city name or falls back to the airport name.
func (a *AirportRef) DisplayCity() string {
	if a == nil {
		return "Unknown"
	}
	if a.City != nil && *a.City != "" {
		return *a.City
	}
	return a.DisplayName()
}

// FlightPosition represents the most recent position of a flight.
type FlightPosition struct {
	FAFlightID     *string   `json:"fa_flight_id"`
	Altitude       int       `json:"altitude"`
	AltitudeChange string    `json:"altitude_change"`
	Groundspeed    int       `json:"groundspeed"`
	Heading        *int      `json:"heading"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	Timestamp      time.Time `json:"timestamp"`
	UpdateType     *string   `json:"update_type"`
}

// Flight represents a flight from the AeroAPI BaseFlight schema.
type Flight struct {
	Ident               string      `json:"ident"`
	IdentICAO           *string     `json:"ident_icao"`
	IdentIATA           *string     `json:"ident_iata"`
	FAFlightID          string      `json:"fa_flight_id"`
	Operator            *string     `json:"operator"`
	OperatorICAO        *string     `json:"operator_icao"`
	OperatorIATA        *string     `json:"operator_iata"`
	FlightNumber        *string     `json:"flight_number"`
	Registration        *string     `json:"registration"`
	ATCIdent            *string     `json:"atc_ident"`
	InboundFAFlightID   *string     `json:"inbound_fa_flight_id"`
	Codeshares          []string    `json:"codeshares"`
	CodesharesIATA      []string    `json:"codeshares_iata"`
	Blocked             bool        `json:"blocked"`
	Diverted            bool        `json:"diverted"`
	Cancelled           bool        `json:"cancelled"`
	PositionOnly        bool        `json:"position_only"`
	Origin              *AirportRef `json:"origin"`
	Destination         *AirportRef `json:"destination"`
	DepartureDelay      *int        `json:"departure_delay"`
	ArrivalDelay        *int        `json:"arrival_delay"`
	FiledETE            *int        `json:"filed_ete"`
	ProgressPercent     *int        `json:"progress_percent"`
	Status              string      `json:"status"`
	AircraftType        *string     `json:"aircraft_type"`
	RouteDistance       *int        `json:"route_distance"`
	FiledAirspeed       *int        `json:"filed_airspeed"`
	FiledAltitude       *int        `json:"filed_altitude"`
	Route               *string     `json:"route"`
	BaggageClaim        *string     `json:"baggage_claim"`
	GateOrigin          *string     `json:"gate_origin"`
	GateDestination     *string     `json:"gate_destination"`
	TerminalOrigin      *string     `json:"terminal_origin"`
	TerminalDestination *string     `json:"terminal_destination"`
	FlightType          string      `json:"type"`
	ScheduledOut        *time.Time  `json:"scheduled_out"`
	EstimatedOut        *time.Time  `json:"estimated_out"`
	ActualOut           *time.Time  `json:"actual_out"`
	ScheduledOff        *time.Time  `json:"scheduled_off"`
	EstimatedOff        *time.Time  `json:"estimated_off"`
	ActualOff           *time.Time  `json:"actual_off"`
	ScheduledOn         *time.Time  `json:"scheduled_on"`
	EstimatedOn         *time.Time  `json:"estimated_on"`
	ActualOn            *time.Time  `json:"actual_on"`
	ScheduledIn         *time.Time  `json:"scheduled_in"`
	EstimatedIn         *time.Time  `json:"estimated_in"`
	ActualIn            *time.Time  `json:"actual_in"`
}

// DisplayIdent returns the best flight identifier for display (prefers IATA).
func (f *Flight) DisplayIdent() string {
	if f.IdentIATA != nil && *f.IdentIATA != "" {
		return *f.IdentIATA
	}
	if f.IdentICAO != nil && *f.IdentICAO != "" {
		return *f.IdentICAO
	}
	return f.Ident
}

// OperatorName returns the best available operator name.
func (f *Flight) OperatorName() string {
	if f.OperatorIATA != nil && *f.OperatorIATA != "" {
		return *f.OperatorIATA
	}
	if f.OperatorICAO != nil && *f.OperatorICAO != "" {
		return *f.OperatorICAO
	}
	if f.Operator != nil && *f.Operator != "" {
		return *f.Operator
	}
	return f.Ident
}

// IsEnRoute returns true if the flight is currently airborne.
func (f *Flight) IsEnRoute() bool {
	return f.ActualOff != nil && f.ActualOn == nil && !f.Cancelled
}

// InFlightPosition represents the position response from /flights/{id}/position.
type InFlightPosition struct {
	Ident             string          `json:"ident"`
	IdentICAO         *string         `json:"ident_icao"`
	IdentIATA         *string         `json:"ident_iata"`
	FAFlightID        string          `json:"fa_flight_id"`
	Origin            *AirportRef     `json:"origin"`
	Destination       *AirportRef     `json:"destination"`
	LastPosition      *FlightPosition `json:"last_position"`
	Waypoints         []float64       `json:"waypoints"`
	FirstPositionTime *time.Time      `json:"first_position_time"`
	AircraftType      *string         `json:"aircraft_type"`
	ActualOff         *time.Time      `json:"actual_off"`
	ActualOn          *time.Time      `json:"actual_on"`
}

// ArrivalsResponse is the response from /airports/{id}/flights/arrivals.
type ArrivalsResponse struct {
	Links    *PaginationLinks `json:"links"`
	NumPages int              `json:"num_pages"`
	Arrivals []Flight         `json:"arrivals"`
}

// DeparturesResponse is the response from /airports/{id}/flights/departures.
type DeparturesResponse struct {
	Links      *PaginationLinks `json:"links"`
	NumPages   int              `json:"num_pages"`
	Departures []Flight         `json:"departures"`
}

// AirportFlightsResponse is the response from /airports/{id}/flights.
// Contains all four categories: scheduled arrivals/departures + completed arrivals/departures.
type AirportFlightsResponse struct {
	Links               *PaginationLinks `json:"links"`
	NumPages            int              `json:"num_pages"`
	ScheduledArrivals   []Flight         `json:"scheduled_arrivals"`
	ScheduledDepartures []Flight         `json:"scheduled_departures"`
	Arrivals            []Flight         `json:"arrivals"`
	Departures          []Flight         `json:"departures"`
}

// PaginationLinks holds pagination cursor links.
type PaginationLinks struct {
	Next *string `json:"next"`
}

// FlightDirection indicates whether a tracked flight is arriving or departing SFO.
type FlightDirection int

const (
	// Arriving means the flight is inbound to SFO.
	Arriving FlightDirection = iota
	// Departing means the flight is outbound from SFO.
	Departing
)

// String returns a human-readable direction label.
func (d FlightDirection) String() string {
	if d == Arriving {
		return "ARRIVING"
	}
	return "DEPARTING"
}

// TrackedFlight holds the current state for the flight being displayed.
type TrackedFlight struct {
	Flight    Flight
	Position  *FlightPosition
	Direction FlightDirection
}
