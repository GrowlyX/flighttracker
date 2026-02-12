package provider

import (
	"fmt"
	"log"
	"sort"
	"time"
)

// providerEntry pairs a provider with its optional rate limit.
type providerEntry struct {
	provider FlightProvider
	limit    *RateLimit // nil = unlimited
}

// MultiProvider tries multiple FlightProviders, selecting by available rate limit capacity.
type MultiProvider struct {
	entries []providerEntry
	// activeIdx tracks which provider last succeeded for position polling.
	activeIdx int
}

// NewMultiProvider creates a provider that selects from the given providers based on rate limits.
func NewMultiProvider(providers ...FlightProvider) *MultiProvider {
	entries := make([]providerEntry, len(providers))
	for i, p := range providers {
		entries[i] = providerEntry{provider: p}
	}
	return &MultiProvider{entries: entries}
}

// SetRateLimit configures a rate limit for a provider by name.
func (m *MultiProvider) SetRateLimit(providerName string, maxReqs int, window time.Duration) {
	for i := range m.entries {
		if m.entries[i].provider.Name() == providerName {
			m.entries[i].limit = NewRateLimit(maxReqs, window)
			log.Printf("[ratelimit] %s: %d requests per %v", providerName, maxReqs, window)
			return
		}
	}
}

func (m *MultiProvider) Name() string {
	return "multi"
}

// sortedByCapacity returns provider indices sorted by descending rate limit capacity.
// Providers with no rate limit (unlimited) are scored at 100%.
// Providers at 0% capacity are excluded.
func (m *MultiProvider) sortedByCapacity() []int {
	type scored struct {
		idx      int
		capacity float64
	}

	var candidates []scored
	for i, e := range m.entries {
		cap := 1.0 // unlimited
		if e.limit != nil {
			cap = e.limit.CapacityPct()
		}
		if cap <= 0 {
			continue // exhausted, skip
		}
		candidates = append(candidates, scored{idx: i, capacity: cap})
	}

	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].capacity > candidates[b].capacity
	})

	indices := make([]int, len(candidates))
	for i, c := range candidates {
		indices[i] = c.idx
	}
	return indices
}

// canUse returns true if the provider at the given index is within its rate limit.
func (m *MultiProvider) canUse(idx int) bool {
	lim := m.entries[idx].limit
	return lim == nil || lim.Allow()
}

// recordUse records a request for the provider at the given index.
func (m *MultiProvider) recordUse(idx int) {
	if lim := m.entries[idx].limit; lim != nil {
		lim.Record()
	}
}

// logRateStatus logs current rate limit status for all providers.
func (m *MultiProvider) logRateStatus() {
	for _, e := range m.entries {
		if e.limit != nil {
			log.Printf("[ratelimit] %s: %d remaining (%.0f%% capacity)",
				e.provider.Name(), e.limit.Remaining(), e.limit.CapacityPct()*100)
		}
	}
}

// GetFlightsNear tries providers sorted by capacity until one returns results.
func (m *MultiProvider) GetFlightsNear(airportICAO string, direction FlightDirection) ([]Flight, error) {
	order := m.sortedByCapacity()
	if len(order) == 0 {
		m.logRateStatus()
		return nil, fmt.Errorf("all providers rate-limited, please wait")
	}

	var lastErr error
	for _, i := range order {
		p := m.entries[i].provider

		if !m.canUse(i) {
			log.Printf("[provider] %s rate-limited, skipping", p.Name())
			continue
		}

		m.recordUse(i)
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

	m.logRateStatus()
	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return nil, nil
}

// GetFlightPosition tries the active provider first (if it has capacity),
// then falls back to others sorted by capacity.
func (m *MultiProvider) GetFlightPosition(flightID string) (*FlightPosition, error) {
	// Try the active provider first if it has capacity.
	if m.activeIdx < len(m.entries) && m.canUse(m.activeIdx) {
		m.recordUse(m.activeIdx)
		pos, err := m.entries[m.activeIdx].provider.GetFlightPosition(flightID)
		if err == nil && pos != nil {
			return pos, nil
		}
		if err != nil {
			log.Printf("[provider] %s failed for GetFlightPosition: %v",
				m.entries[m.activeIdx].provider.Name(), err)
		}
	} else if m.activeIdx < len(m.entries) && !m.canUse(m.activeIdx) {
		log.Printf("[provider] %s rate-limited for position, falling back",
			m.entries[m.activeIdx].provider.Name())
	}

	// Fall back to others sorted by capacity.
	order := m.sortedByCapacity()
	var lastErr error
	for _, i := range order {
		if i == m.activeIdx {
			continue
		}
		p := m.entries[i].provider
		if !m.canUse(i) {
			continue
		}

		m.recordUse(i)
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

	m.logRateStatus()
	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed for position, last error: %w", lastErr)
	}
	return nil, fmt.Errorf("no position available (providers may be rate-limited)")
}
