package tracker

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/subham/flighttracker/internal/provider"
)

const (
	airportCode  = "KSFO"
	pollInterval = 8 * time.Second // how often we refresh the flight list + featured position
	maxDistNM    = 50.0            // radar radius in nautical miles
)

// FlightWithPos bundles a flight with its latest known position.
type FlightWithPos struct {
	Flight   *provider.Flight
	Position *provider.FlightPosition
}

// State holds the radar snapshot for the UI.
type State struct {
	AllFlights    []FlightWithPos // every flight in the radar zone
	Featured      *FlightWithPos  // the one shown in the sidebar
	FeaturedIdent string          // ident of the featured flight (for identity)
	Error         string
	UpdatedAt     time.Time
}

// Tracker manages the radar-style flight tracking.
type Tracker struct {
	prov provider.FlightProvider

	mu    sync.RWMutex
	state State

	// Featured flight management
	featuredIdent string // ident we're sticking with
	staleCount    int    // consecutive polls where featured was stationary
	direction     provider.FlightDirection

	// AirlineFilter is an optional callback that returns true if the airline
	// code/name is known. Flights failing this check are skipped.
	AirlineFilter func(iata, name string) bool
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

// Run starts the radar loop. Blocks forever — run in a goroutine.
func (t *Tracker) Run() {
	for {
		t.radarTick()
		time.Sleep(pollInterval)
	}
}

// radarTick performs one refresh cycle:
// 1. Fetch all nearby flights (alternating departures/arrivals)
// 2. Filter to known airlines within radar range
// 3. Poll position for the featured flight
// 4. Manage featured flight selection
func (t *Tracker) radarTick() {
	// Alternate direction each tick to get both arrivals and departures
	if t.direction == provider.Departing {
		t.direction = provider.Arriving
	} else {
		t.direction = provider.Departing
	}

	flights, err := t.prov.GetFlightsNear(airportCode, t.direction)
	if err != nil {
		log.Printf("[tracker] error fetching flights: %v", err)
		return
	}

	// Also fetch the other direction
	otherDir := provider.Departing
	if t.direction == provider.Departing {
		otherDir = provider.Arriving
	}
	otherFlights, err2 := t.prov.GetFlightsNear(airportCode, otherDir)
	if err2 == nil {
		flights = append(flights, otherFlights...)
	}

	// Deduplicate by ident
	seen := make(map[string]bool)
	var deduped []provider.Flight
	for _, f := range flights {
		key := f.Ident
		if key == "" {
			key = f.FlightID
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, f)
	}
	flights = deduped

	log.Printf("[tracker] radar: %d flights nearby", len(flights))

	// Filter and build FlightWithPos list
	var allFlights []FlightWithPos
	var featuredFWP *FlightWithPos

	for i := range flights {
		f := &flights[i]

		// Must be airborne with an ident
		if !f.IsAirborne || f.Ident == "" {
			continue
		}

		// Filter to known airlines only
		if t.AirlineFilter != nil {
			if !t.AirlineFilter(f.OperatorIATA, f.Operator) {
				continue
			}
		}

		fwp := FlightWithPos{Flight: f}

		// If this is the featured flight, poll its position
		if f.Ident == t.featuredIdent || f.FlightID == t.featuredIdent {
			pos, err := t.prov.GetFlightPosition(f)
			if err == nil && pos != nil {
				fwp.Position = pos

				// Check if stationary (groundspeed == 0)
				if pos.Groundspeed == 0 {
					t.staleCount++
				} else {
					t.staleCount = 0
				}

				// Check if out of radar range
				if pos.Latitude != 0 && pos.Longitude != 0 {
					dist := haversineNM(sfoLat, sfoLon, pos.Latitude, pos.Longitude)
					if dist > maxDistNM {
						log.Printf("[tracker] featured %s left radar (%.0fnm), switching", f.DisplayIdent(), dist)
						t.featuredIdent = ""
						t.staleCount = 0
					}
				}
			}
			if t.featuredIdent != "" {
				featuredFWP = &fwp
			}
		}

		allFlights = append(allFlights, fwp)
	}

	// If featured is stale for >2 polls, drop it
	if t.staleCount > 2 {
		log.Printf("[tracker] featured %s stationary for %d polls, switching", t.featuredIdent, t.staleCount)
		t.featuredIdent = ""
		t.staleCount = 0
		featuredFWP = nil
	}

	// If no featured, pick a new one (any moving airline flight)
	if t.featuredIdent == "" && len(allFlights) > 0 {
		for i := range allFlights {
			f := allFlights[i].Flight
			t.featuredIdent = f.Ident
			if t.featuredIdent == "" {
				t.featuredIdent = f.FlightID
			}
			log.Printf("[tracker] featured → %s", f.DisplayIdent())

			// Backfill aircraft type if missing
			if f.AircraftType == "" && f.FlightID != "" {
				go t.backfillAircraftType(f)
			}

			// Poll position for the newly featured flight
			pos, err := t.prov.GetFlightPosition(f)
			if err == nil && pos != nil {
				allFlights[i].Position = pos
			}
			featuredFWP = &allFlights[i]
			break
		}
	}

	t.setState(State{
		AllFlights:    allFlights,
		Featured:      featuredFWP,
		FeaturedIdent: t.featuredIdent,
	})
}

// SFO coordinates for distance filtering.
const (
	sfoLat = 37.6213
	sfoLon = -122.3790
)

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

// hexdbResponse represents the hexdb.io aircraft lookup response.
type hexdbResponse struct {
	ICAOTypeCode     string `json:"ICAOTypeCode"` // e.g. "A359", "B738"
	Type             string `json:"Type"`         // e.g. "Airbus A350-900"
	RegisteredOwners string `json:"RegisteredOwners"`
}

// backfillAircraftType looks up the aircraft type from the ICAO24 hex code
// using the free hexdb.io API and updates the flight.
func (t *Tracker) backfillAircraftType(flight *provider.Flight) {
	icao24 := flight.FlightID
	if len(icao24) != 6 {
		return // not a valid ICAO24 hex
	}

	apiURL := fmt.Sprintf("https://hexdb.io/api/v1/aircraft/%s", icao24)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(apiURL)
	if err != nil {
		log.Printf("[tracker] hexdb lookup error for %s: %v", icao24, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[tracker] hexdb HTTP %d for %s", resp.StatusCode, icao24)
		return
	}

	var result hexdbResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[tracker] hexdb decode error for %s: %v", icao24, err)
		return
	}

	if result.ICAOTypeCode == "" {
		return
	}

	log.Printf("[tracker] backfilled aircraft type for %s: %s", flight.DisplayIdent(), result.ICAOTypeCode)
	flight.AircraftType = result.ICAOTypeCode
}
