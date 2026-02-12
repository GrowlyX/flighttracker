package tracker

import (
	"log"
	"sync"
	"time"

	"github.com/subham/flighttracker/internal/aeroapi"
)

const (
	airportCode            = "KSFO"
	positionPollInterval   = 1 * time.Second
	flightListPollInterval = 1 * time.Second
)

// State holds a snapshot of the currently tracked flight for the UI to read.
type State struct {
	Flight    *aeroapi.Flight
	Position  *aeroapi.FlightPosition
	Direction aeroapi.FlightDirection
	Error     string
	UpdatedAt time.Time
}

// Tracker manages the flight tracking state machine.
type Tracker struct {
	client *aeroapi.Client

	mu    sync.RWMutex
	state State

	// Current tracking context
	currentFlightID string
	direction       aeroapi.FlightDirection
}

// New creates a new Tracker with the given AeroAPI client.
func New(client *aeroapi.Client) *Tracker {
	return &Tracker{
		client:    client,
		direction: aeroapi.Arriving,
	}
}

// GetState returns a copy of the current tracking state (thread-safe).
func (t *Tracker) GetState() State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

func (t *Tracker) setState(s State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s.UpdatedAt = time.Now()
	t.state = s
}

func (t *Tracker) setError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Error = err.Error()
	t.state.UpdatedAt = time.Now()
}

// Run starts the tracking loop. It blocks forever — run in a goroutine.
func (t *Tracker) Run() {
	for {
		t.runCycle()
	}
}

// runCycle performs one full arrive→depart cycle.
func (t *Tracker) runCycle() {
	// Phase 1: Track an arriving flight
	t.direction = aeroapi.Arriving
	t.trackNextFlight()

	// Phase 2: Track a departing flight
	t.direction = aeroapi.Departing
	t.trackNextFlight()
}

// trackNextFlight finds a suitable en-route flight and tracks it until completion.
func (t *Tracker) trackNextFlight() {
	for {
		flight := t.findEnRouteFlight()
		if flight == nil {
			log.Printf("[tracker] no en-route %s flights found, retrying in %v", t.direction, flightListPollInterval)
			time.Sleep(flightListPollInterval)
			continue
		}

		log.Printf("[tracker] tracking %s flight %s (%s)", t.direction, flight.DisplayIdent(), flight.FAFlightID)
		t.currentFlightID = flight.FAFlightID

		// Set initial state with flight info
		t.setState(State{
			Flight:    flight,
			Direction: t.direction,
		})

		// Poll position until the flight completes
		completed := t.pollUntilComplete(flight)
		if completed {
			log.Printf("[tracker] flight %s completed, switching", flight.DisplayIdent())
			// Brief pause to show final state
			time.Sleep(3 * time.Second)
			return
		}
		// If not completed (lost tracking), find another flight
	}
}

// findEnRouteFlight searches for an active in-flight aircraft.
func (t *Tracker) findEnRouteFlight() *aeroapi.Flight {
	var flights []aeroapi.Flight
	var err error

	if t.direction == aeroapi.Arriving {
		flights, err = t.client.GetArrivals(airportCode)
	} else {
		flights, err = t.client.GetDepartures(airportCode)
	}
	if err != nil {
		log.Printf("[tracker] error fetching flights: %v", err)
		t.setError(err)
		return nil
	}

	// Find the first en-route flight
	for i := range flights {
		f := &flights[i]
		if f.IsEnRoute() && f.FlightType == "Airline" {
			return f
		}
	}

	// Fallback: any en-route flight
	for i := range flights {
		f := &flights[i]
		if f.IsEnRoute() {
			return f
		}
	}

	return nil
}

// pollUntilComplete polls the flight position every second until it lands/departs.
// Returns true if the flight has completed, false if tracking was lost.
func (t *Tracker) pollUntilComplete(flight *aeroapi.Flight) bool {
	ticker := time.NewTicker(positionPollInterval)
	defer ticker.Stop()

	consecutiveErrors := 0
	const maxErrors = 30

	for range ticker.C {
		pos, err := t.client.GetFlightPosition(flight.FAFlightID)
		if err != nil {
			consecutiveErrors++
			log.Printf("[tracker] position error (%d/%d): %v", consecutiveErrors, maxErrors, err)
			t.setError(err)
			if consecutiveErrors >= maxErrors {
				return false
			}
			continue
		}
		consecutiveErrors = 0

		// Check if flight has completed
		if pos.ActualOn != nil && t.direction == aeroapi.Arriving {
			// Arrival has landed
			if pos.LastPosition != nil {
				t.setState(State{
					Flight:    flight,
					Position:  pos.LastPosition,
					Direction: t.direction,
				})
			}
			return true
		}
		if t.direction == aeroapi.Departing && pos.LastPosition != nil {
			// For departures, track until they're well airborne (altitude > 100 = 10,000ft)
			// or we've been tracking for 5+ minutes
			if pos.LastPosition.Altitude > 100 {
				t.setState(State{
					Flight:    flight,
					Position:  pos.LastPosition,
					Direction: t.direction,
				})
				return true
			}
		}

		// Update the current position
		if pos.LastPosition != nil {
			t.setState(State{
				Flight:    flight,
				Position:  pos.LastPosition,
				Direction: t.direction,
			})
		}
	}
	return false
}
