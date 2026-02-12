package provider

import "time"

// FlightDirection indicates whether a tracked flight is arriving or departing.
type FlightDirection int

const (
	Arriving FlightDirection = iota
	Departing
)

func (d FlightDirection) String() string {
	if d == Arriving {
		return "ARRIVING"
	}
	return "DEPARTING"
}

// AirportRef represents a reference to an airport.
type AirportRef struct {
	Code     string
	CodeICAO string
	CodeIATA string
	Name     string
	City     string
}

// DisplayCode returns the best available airport code for display.
func (a *AirportRef) DisplayCode() string {
	if a == nil {
		return "???"
	}
	if a.CodeIATA != "" {
		return a.CodeIATA
	}
	if a.CodeICAO != "" {
		return a.CodeICAO
	}
	if a.Code != "" {
		return a.Code
	}
	return "???"
}

// DisplayCity returns the city name or falls back to the airport name.
func (a *AirportRef) DisplayCity() string {
	if a == nil {
		return "Unknown"
	}
	if a.City != "" {
		return a.City
	}
	if a.Name != "" {
		return a.Name
	}
	return a.DisplayCode()
}

// FlightPosition represents the current position of a flight.
type FlightPosition struct {
	Altitude       int
	AltitudeChange string // "C" = climbing, "D" = descending, "-" = level
	Groundspeed    int    // knots
	Heading        *int
	Latitude       float64
	Longitude      float64
	Timestamp      time.Time
}

// Flight represents a flight.
type Flight struct {
	Ident        string
	IdentICAO    string
	IdentIATA    string
	FlightID     string // provider-specific unique ID
	Operator     string
	OperatorICAO string
	OperatorIATA string
	FlightNumber string
	Origin       *AirportRef
	Destination  *AirportRef
	Status       string
	AircraftType string
	IsAirborne   bool
}

// DisplayIdent returns the best flight identifier for display (prefers IATA).
func (f *Flight) DisplayIdent() string {
	if f.IdentIATA != "" {
		return f.IdentIATA
	}
	if f.IdentICAO != "" {
		return f.IdentICAO
	}
	return f.Ident
}

// OperatorName returns the best available operator name.
func (f *Flight) OperatorName() string {
	if f.OperatorIATA != "" {
		return f.OperatorIATA
	}
	if f.OperatorICAO != "" {
		return f.OperatorICAO
	}
	if f.Operator != "" {
		return f.Operator
	}
	return f.Ident
}
