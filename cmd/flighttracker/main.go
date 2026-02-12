package main

import (
	"log"
	"os"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/subham/flighttracker/internal/aeroapi"
	"github.com/subham/flighttracker/internal/tracker"
	"github.com/subham/flighttracker/internal/ui"
)

func main() {
	apiKey := os.Getenv("AEROAPI_KEY")

	log.Println("SFO Flight Tracker starting...")
	log.Printf("Using API key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])

	// Create AeroAPI client
	client := aeroapi.NewClient(apiKey)

	// Create tracker
	t := tracker.New(client)

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
