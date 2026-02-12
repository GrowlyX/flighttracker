package tracker

import (
	"log"
	"sync"
	"time"

	"github.com/subham/flighttracker/internal/provider"
)

const (
	airportCode            = "KSFO"
	positionPollInterval   = 3 * time.Second
	flightListPollInterval = 3 * time.Second
)

// State holds a snapshot of the currently tracked flight for the UI to read.
type State struct {
	Flight    *provider.Flight
	Position  *provider.FlightPosition
	Direction provider.FlightDirection
	Error     string
	UpdatedAt time.Time
}

// Tracker manages the flight tracking state machine.
type Tracker struct {
	prov provider.FlightProvider

	mu    sync.RWMutex
	state State

	// Current tracking context
	currentFlightID string
	direction       provider.FlightDirection
}

// New creates a new Tracker with the given flight provider.
func New(prov provider.FlightProvider) *Tracker {
	return &Tracker{
		prov:      prov,
		direction: provider.Departing,
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
	// Track a departing flight
	t.direction = provider.Departing
	t.trackNextFlight()

	// Track an arriving flight
	t.direction = provider.Arriving
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

		log.Printf("[tracker] tracking %s flight %s (%s)", t.direction, flight.DisplayIdent(), flight.FlightID)
		t.currentFlightID = flight.FlightID

		// Set initial state with flight info
		t.setState(State{
			Flight:    flight,
			Direction: t.direction,
		})

		// Poll position until the flight completes
		completed := t.pollUntilComplete(flight)
		if completed {
			log.Printf("[tracker] flight %s completed, switching", flight.DisplayIdent())
			time.Sleep(3 * time.Second)
			return
		}
	}
}

// findEnRouteFlight searches for an active in-flight aircraft.
func (t *Tracker) findEnRouteFlight() *provider.Flight {
	flights, err := t.prov.GetFlightsNear(airportCode, t.direction)
	if err != nil {
		log.Printf("[tracker] error fetching flights: %v", err)
		t.setError(err)
		return nil
	}

	// First pass: airborne airline flights
	for i := range flights {
		f := &flights[i]
		if f.IsAirborne && isAirline(f) {
			return f
		}
	}
	// Second pass: any airborne flight
	for i := range flights {
		f := &flights[i]
		if f.IsAirborne {
			return f
		}
	}
	return nil
}

func isAirline(f *provider.Flight) bool {
	return f.OperatorIATA != "" || f.OperatorICAO != "" || f.Operator != ""
}

// pollUntilComplete polls the flight position until it lands/departs.
func (t *Tracker) pollUntilComplete(flight *provider.Flight) bool {
	ticker := time.NewTicker(positionPollInterval)
	defer ticker.Stop()

	consecutiveErrors := 0
	const maxErrors = 30

	for range ticker.C {
		pos, err := t.prov.GetFlightPosition(flight.FlightID)
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

		// For departures, track until they're well airborne (altitude > 100 = 10,000ft)
		if t.direction == provider.Departing && pos.Altitude > 100 {
			t.setState(State{
				Flight:    flight,
				Position:  pos,
				Direction: t.direction,
			})
			return true
		}

		// For arrivals, track until altitude drops to 0 or very low
		if t.direction == provider.Arriving && pos.Altitude <= 0 {
			t.setState(State{
				Flight:    flight,
				Position:  pos,
				Direction: t.direction,
			})
			return true
		}

		// Update the current position
		t.setState(State{
			Flight:    flight,
			Position:  pos,
			Direction: t.direction,
		})
	}
	return false
}
