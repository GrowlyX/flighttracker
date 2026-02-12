package provider

// FlightProvider is the interface for all flight data sources.
type FlightProvider interface {
	// Name returns a human-readable provider name for logging.
	Name() string

	// GetFlightsNear returns en-route flights near the given airport.
	GetFlightsNear(airportICAO string, direction FlightDirection) ([]Flight, error)

	// GetFlightPosition returns the latest position for a flight by its provider-specific ID.
	GetFlightPosition(flightID string) (*FlightPosition, error)
}
