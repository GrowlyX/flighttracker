package provider

import (
	"fmt"
	"log"
)

// MultiProvider tries multiple FlightProviders in order (waterfall failover).
type MultiProvider struct {
	providers []FlightProvider
	// activeIdx tracks which provider last succeeded for position polling.
	activeIdx int
}

// NewMultiProvider creates a provider that waterfalls through the given providers.
func NewMultiProvider(providers ...FlightProvider) *MultiProvider {
	return &MultiProvider{providers: providers}
}

func (m *MultiProvider) Name() string {
	return "multi"
}

// GetFlightsNear tries each provider in order until one returns results.
func (m *MultiProvider) GetFlightsNear(airportICAO string, direction FlightDirection) ([]Flight, error) {
	var lastErr error
	for i, p := range m.providers {
		flights, err := p.GetFlightsNear(airportICAO, direction)
		if err != nil {
			log.Printf("[provider] %s failed for GetFlightsNear: %v", p.Name(), err)
			lastErr = err
			continue
		}
		if len(flights) == 0 {
			log.Printf("[provider] %s returned 0 flights, trying next", p.Name())
			continue
		}
		log.Printf("[provider] %s returned %d flights", p.Name(), len(flights))
		m.activeIdx = i
		return flights, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return nil, nil
}

// GetFlightPosition tries the active provider first, then falls back to others.
func (m *MultiProvider) GetFlightPosition(flightID string) (*FlightPosition, error) {
	// Try the active provider first (the one that found the flight).
	if m.activeIdx < len(m.providers) {
		pos, err := m.providers[m.activeIdx].GetFlightPosition(flightID)
		if err == nil && pos != nil {
			return pos, nil
		}
		log.Printf("[provider] %s failed for GetFlightPosition: %v", m.providers[m.activeIdx].Name(), err)
	}

	// Fall back to others.
	var lastErr error
	for i, p := range m.providers {
		if i == m.activeIdx {
			continue
		}
		pos, err := p.GetFlightPosition(flightID)
		if err != nil {
			lastErr = err
			continue
		}
		if pos != nil {
			m.activeIdx = i
			return pos, nil
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed for position, last error: %w", lastErr)
	}
	return nil, fmt.Errorf("no position available")
}
