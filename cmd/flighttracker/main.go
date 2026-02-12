package main

import (
	"log"
	"os"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/subham/flighttracker/internal/provider"
	"github.com/subham/flighttracker/internal/tracker"
	"github.com/subham/flighttracker/internal/ui"
)

func main() {
	log.Println("SFO Flight Tracker starting...")

	// Build provider chain (waterfall: AeroAPI → OpenSky → AviationStack)
	var providers []provider.FlightProvider

	// 1. AeroAPI (best data, paid)
	if key := os.Getenv("AEROAPI_KEY"); key != "" {
		log.Printf("AeroAPI: enabled (key: %s...%s)", key[:4], key[len(key)-4:])
		providers = append(providers, provider.NewAeroAPIProvider(key))
	}

	// 2. OpenSky Network (free, no key required)
	openskyUser := os.Getenv("OPENSKY_USER")
	openskyPass := os.Getenv("OPENSKY_PASS")
	log.Printf("OpenSky: enabled (auth: %v)", openskyUser != "")
	providers = append(providers, provider.NewOpenSkyProvider(openskyUser, openskyPass))

	// 3. AviationStack (free tier: 100 req/month)
	if key := os.Getenv("AVIATIONSTACK_KEY"); key != "" {
		log.Printf("AviationStack: enabled")
		providers = append(providers, provider.NewAviationStackProvider(key))
	}

	if len(providers) == 0 {
		log.Fatal("No flight providers configured. Set AEROAPI_KEY, OPENSKY_USER, or AVIATIONSTACK_KEY.")
	}

	log.Printf("Provider chain: %d providers configured", len(providers))

	// Create multi-provider with waterfall failover
	prov := provider.NewMultiProvider(providers...)

	// Create tracker
	t := tracker.New(prov)

	// Start tracker in background
	go t.Run()

	// Create and run the Ebitengine game
	game := ui.NewGame(t)

	ebiten.SetWindowTitle("SFO Flight Tracker")
	ebiten.SetWindowSize(800, 480)
	ebiten.SetTPS(30)
	ebiten.SetVsyncEnabled(true)

	if err := ebiten.RunGame(game); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}
