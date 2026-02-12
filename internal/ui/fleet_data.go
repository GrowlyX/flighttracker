package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const aerolopaBaseURL = "https://www.aerolopa.com/dummyversion/v1/airlines"

// fleetAircraft represents a single aircraft type from the aerolopa API.
type fleetAircraft struct {
	CodeDisplayed string `json:"aircraft_code_displayed"`
	TypeDisplayed string `json:"aircraft_type_displayed"`
	Image         string `json:"image"`
}

// fleetBody represents a body group (wide/narrow) from the aerolopa API.
type fleetBody struct {
	Name      string          `json:"name"`
	Aircrafts []fleetAircraft `json:"aircrafts"`
}

// fleetResponse represents the aerolopa API response.
type fleetResponse struct {
	Name   string      `json:"name"`
	Slug   string      `json:"slug"`
	Bodies []fleetBody `json:"bodies"`
}

// fleetCache stores aircraft type lookups per airline slug.
var fleetCache sync.Map // map[string]map[string]string (slug → (code → type))

// fleetFetching tracks in-progress fetches to avoid duplicates.
var fleetFetching sync.Map

// cacheDir returns the fleet cache directory path.
func cacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".flighttracker", "cache", "airlines")
}

// LookupAircraftType returns the aircraft type display name for a given airline and aircraft code.
// It triggers a background fetch if the data isn't cached yet.
func LookupAircraftType(airlineIATA string, aircraftCode string) string {
	slug := strings.ToLower(airlineIATA)
	if slug == "" || aircraftCode == "" {
		return ""
	}

	// Check in-memory cache first
	if cached, ok := fleetCache.Load(slug); ok {
		if typeMap, ok := cached.(map[string]string); ok {
			codeUpper := strings.ToUpper(aircraftCode)
			if t, ok := typeMap[codeUpper]; ok {
				return t
			}
			// Try partial match (e.g. "B738" → look for entries containing "738")
			for code, typeName := range typeMap {
				if strings.Contains(codeUpper, code) || strings.Contains(code, codeUpper) {
					return typeName
				}
			}
		}
		return "" // cached but no match
	}

	// Try loading from disk cache
	if loadFromDisk(slug) {
		return LookupAircraftType(airlineIATA, aircraftCode) // retry after loading
	}

	// Trigger background fetch
	go fetchFleetData(slug)
	return ""
}

// fetchFleetData fetches fleet data from the aerolopa API and caches it.
func fetchFleetData(slug string) {
	// Prevent duplicate fetches
	if _, loaded := fleetFetching.LoadOrStore(slug, true); loaded {
		return
	}
	defer fleetFetching.Delete(slug)

	apiURL := fmt.Sprintf("%s/%s", aerolopaBaseURL, slug)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(apiURL)
	if err != nil {
		log.Printf("[fleet] error fetching %s: %v", slug, err)
		fleetCache.Store(slug, map[string]string{}) // cache empty to avoid retries
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[fleet] HTTP %d for %s", resp.StatusCode, slug)
		fleetCache.Store(slug, map[string]string{})
		return
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		log.Printf("[fleet] read error for %s: %v", slug, err)
		fleetCache.Store(slug, map[string]string{})
		return
	}

	// Parse and build type map
	var fleet fleetResponse
	if err := json.Unmarshal(data, &fleet); err != nil {
		log.Printf("[fleet] parse error for %s: %v", slug, err)
		fleetCache.Store(slug, map[string]string{})
		return
	}

	typeMap := buildTypeMap(&fleet)
	fleetCache.Store(slug, typeMap)
	log.Printf("[fleet] cached %d aircraft types for %s", len(typeMap), slug)

	// Save to disk
	saveToDisk(slug, data)
}

// buildTypeMap creates a mapping of aircraft code → display type from fleet data.
func buildTypeMap(fleet *fleetResponse) map[string]string {
	typeMap := make(map[string]string)
	for _, body := range fleet.Bodies {
		for _, ac := range body.Aircrafts {
			if ac.CodeDisplayed != "" && ac.TypeDisplayed != "" {
				typeMap[strings.ToUpper(ac.CodeDisplayed)] = ac.TypeDisplayed
			}
		}
	}
	return typeMap
}

// saveToDisk writes fleet JSON to the disk cache.
func saveToDisk(slug string, data []byte) {
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	path := filepath.Join(dir, slug+".json")
	_ = os.WriteFile(path, data, 0644)
}

// loadFromDisk loads fleet data from the disk cache.
func loadFromDisk(slug string) bool {
	path := filepath.Join(cacheDir(), slug+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var fleet fleetResponse
	if err := json.Unmarshal(data, &fleet); err != nil {
		return false
	}

	typeMap := buildTypeMap(&fleet)
	fleetCache.Store(slug, typeMap)
	return true
}
