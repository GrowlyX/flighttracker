package ui

import (
	"encoding/json"
	"log"
	"strings"

	_ "embed"
)

//go:embed assets/airlines.json
var airlinesJSON []byte

// AirlineEntry represents a single airline from the dataset.
type AirlineEntry struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`       // lowercase IATA 2-letter code
	CardImage string `json:"card_image"` // airline tile image (300x201)
	LogoImage string `json:"logo_image"` // alliance logo (Star/SkyTeam/oneworld)
}

// airlineByIATA maps lowercase IATA code → AirlineEntry.
var airlineByIATA map[string]AirlineEntry

// airlineByNameLower maps lowercase airline name → AirlineEntry for fuzzy fallback.
var airlineByNameLower map[string]AirlineEntry

func init() {
	var data struct {
		Airlines []AirlineEntry `json:"airlines"`
	}
	if err := json.Unmarshal(airlinesJSON, &data); err != nil {
		log.Printf("[ui] warning: could not parse airlines.json: %v", err)
		return
	}

	airlineByIATA = make(map[string]AirlineEntry, len(data.Airlines))
	airlineByNameLower = make(map[string]AirlineEntry, len(data.Airlines))

	for _, a := range data.Airlines {
		slug := strings.ToLower(a.Slug)
		if slug != "" {
			airlineByIATA[slug] = a
		}
		nameLower := strings.ToLower(a.Name)
		if nameLower != "" {
			airlineByNameLower[nameLower] = a
		}
	}
	log.Printf("[ui] loaded %d airlines from dataset", len(airlineByIATA))
}

// LookupAirlineByIATA returns the airline entry for a given IATA code (case-insensitive).
func LookupAirlineByIATA(iataCode string) (AirlineEntry, bool) {
	a, ok := airlineByIATA[strings.ToLower(iataCode)]
	return a, ok
}

// LookupAirlineByName returns the airline entry matching the given name (case-insensitive).
func LookupAirlineByName(name string) (AirlineEntry, bool) {
	a, ok := airlineByNameLower[strings.ToLower(name)]
	return a, ok
}
