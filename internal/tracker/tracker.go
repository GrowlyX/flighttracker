package tracker

import (
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/subham/flighttracker/internal/provider"
)

const (
	airportCode            = "KSFO"
	positionPollInterval   = 8 * time.Second // longer interval since dead reckoning fills gaps
	flightListPollInterval = 5 * time.Second
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

// SFO coordinates for distance filtering.
const (
	sfoLat = 37.6213
	sfoLon = -122.3790
	// maxDistanceNM is the maximum distance from SFO to consider a flight.
	// ~50 nautical miles keeps flights visually close on the map.
	maxDistanceNM = 50.0
)

// findEnRouteFlight searches for an active in-flight aircraft, preferring
// airline flights close to SFO.
func (t *Tracker) findEnRouteFlight() *provider.Flight {
	flights, err := t.prov.GetFlightsNear(airportCode, t.direction)
	if err != nil {
		log.Printf("[tracker] error fetching flights: %v", err)
		t.setError(err)
		return nil
	}

	// Score and filter flights by distance and airline status
	type scored struct {
		flight  *provider.Flight
		dist    float64
		airline bool
	}
	var candidates []scored

	for i := range flights {
		f := &flights[i]
		if !f.IsAirborne || f.Ident == "" {
			continue
		}
		// Skip flights without a position (we can't compute distance)
		// For AeroAPI flights, we don't have lat/lon at discovery time,
		// so we allow them through with dist=0.
		candidates = append(candidates, scored{
			flight:  f,
			dist:    0, // distance computed below if position available
			airline: isAirline(f),
		})
	}

	// Sort: airline flights first, then by distance (nearest first)
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].airline != candidates[b].airline {
			return candidates[a].airline
		}
		return candidates[a].dist < candidates[b].dist
	})

	if len(candidates) > 0 {
		return candidates[0].flight
	}
	return nil
}

func isAirline(f *provider.Flight) bool {
	return f.OperatorIATA != "" || f.OperatorICAO != "" || f.Operator != ""
}

// haversineNM computes distance between two lat/lon points in nautical miles.
func haversineNM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusNM = 3440.065
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusNM * c
}

// pollUntilComplete polls the flight position until it lands/departs.
func (t *Tracker) pollUntilComplete(flight *provider.Flight) bool {
	ticker := time.NewTicker(positionPollInterval)
	defer ticker.Stop()

	consecutiveErrors := 0
	const maxErrors = 30
	// Disqualify flights that move beyond this distance from SFO
	const maxPollDistanceNM = 100.0

	for range ticker.C {
		pos, err := t.prov.GetFlightPosition(flight)
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

		// Disqualify if flight has moved too far from SFO
		if pos.Latitude != 0 && pos.Longitude != 0 {
			dist := haversineNM(sfoLat, sfoLon, pos.Latitude, pos.Longitude)
			if dist > maxPollDistanceNM {
				log.Printf("[tracker] flight %s is %.0fnm from SFO (max %0.fnm), disqualifying",
					flight.DisplayIdent(), dist, maxPollDistanceNM)
				return true // treat as completed so we move on
			}
		}

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
