package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"github.com/subham/flighttracker/internal/aeroapi"
	"github.com/subham/flighttracker/internal/tracker"

	// Image decoders
	_ "image/jpeg"
	_ "image/png"
)

const (
	screenWidth  = 800
	screenHeight = 480
	targetTPS    = 30
)

// AirlineInfo maps ICAO/IATA codes to display names.
var airlineNames = map[string]string{
	"UAL": "United Airlines", "AAL": "American Airlines", "DAL": "Delta Air Lines",
	"SWA": "Southwest Airlines", "ASA": "Alaska Airlines", "JBU": "JetBlue Airways",
	"NKS": "Spirit Airlines", "FFT": "Frontier Airlines", "HAL": "Hawaiian Airlines",
	"SKW": "SkyWest Airlines", "RPA": "Republic Airways", "ENY": "Envoy Air",
	"PDT": "Piedmont Airlines", "PSA": "PSA Airlines", "MXY": "Breeze Airways",
	"AFR": "Air France", "BAW": "British Airways", "DLH": "Lufthansa",
	"KLM": "KLM", "ANA": "All Nippon Airways", "JAL": "Japan Airlines",
	"CPA": "Cathay Pacific", "SIA": "Singapore Airlines", "QFA": "Qantas",
	"EVA": "EVA Air", "CAL": "China Airlines", "CCA": "Air China",
	"CSN": "China Southern", "CES": "China Eastern", "KAL": "Korean Air",
	"THA": "Thai Airways", "UAE": "Emirates", "ETD": "Etihad Airways",
	"THY": "Turkish Airlines", "ACA": "Air Canada", "AMX": "Aeromexico",
	"VIR": "Virgin Atlantic", "FIN": "Finnair", "SAS": "Scandinavian Airlines",
	"TAP": "TAP Air Portugal", "IBE": "Iberia", "AZA": "ITA Airways",
	"WJA": "WestJet", "CMP": "Copa Airlines", "AVA": "Avianca",
	"LAN": "LATAM Airlines", "VOI": "Volaris", "VRD": "Virgin America",
}

// Game implements ebiten.Game for the flight tracker display.
type Game struct {
	tracker    *tracker.Tracker
	mapRender  *MapRenderer
	logoCache  sync.Map // map[string]*ebiten.Image
	fontFace   *text.GoTextFace
	fontFaceSm *text.GoTextFace
	fontFaceLg *text.GoTextFace
	fontFaceXl *text.GoTextFace
}

// NewGame creates a new Game instance wired to the given tracker.
func NewGame(t *tracker.Tracker) *Game {
	g := &Game{
		tracker:   t,
		mapRender: NewMapRenderer(0, 90, screenWidth, screenHeight-150),
	}
	g.initFonts()
	return g
}

// initFonts sets up the font faces using the Go built-in fonts.
func (g *Game) initFonts() {
	source, err := text.NewGoTextFaceSource(defaultFontData())
	if err != nil {
		log.Printf("[ui] error loading font: %v, using fallback", err)
		return
	}

	g.fontFaceSm = &text.GoTextFace{Source: source, Size: 14}
	g.fontFace = &text.GoTextFace{Source: source, Size: 18}
	g.fontFaceLg = &text.GoTextFace{Source: source, Size: 28}
	g.fontFaceXl = &text.GoTextFace{Source: source, Size: 36}
}

// Update is called every tick (30 TPS).
func (g *Game) Update() error {
	state := g.tracker.GetState()
	if state.Position != nil {
		g.mapRender.Update(state.Position.Latitude, state.Position.Longitude)
	}
	return nil
}

// Draw renders the entire screen.
func (g *Game) Draw(screen *ebiten.Image) {
	// Background
	screen.Fill(color.RGBA{0x08, 0x0c, 0x15, 0xff})

	state := g.tracker.GetState()

	if state.Flight == nil {
		g.drawWaiting(screen, state)
		return
	}

	// Draw sections
	g.drawHeader(screen, state)
	g.drawMap(screen, state)
	g.drawBottomBar(screen, state)
}

// Layout returns the logical screen dimensions.
func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

// drawWaiting shows a loading/searching screen.
func (g *Game) drawWaiting(screen *ebiten.Image, state tracker.State) {
	if g.fontFaceLg == nil {
		return
	}

	msg := "Searching for flights at SFO..."
	if state.Error != "" {
		msg = "Connection issue, retrying..."
	}

	// Pulsing circle
	vector.DrawFilledCircle(screen, screenWidth/2, screenHeight/2-30, 20, color.RGBA{0x00, 0x96, 0xff, 0x60}, false)
	vector.DrawFilledCircle(screen, screenWidth/2, screenHeight/2-30, 10, color.RGBA{0x00, 0x96, 0xff, 0xff}, false)

	op := &text.DrawOptions{}
	op.GeoM.Translate(screenWidth/2, screenHeight/2+20)
	op.ColorScale.ScaleWithColor(color.RGBA{0x88, 0x99, 0xaa, 0xff})
	op.PrimaryAlign = text.AlignCenter
	text.Draw(screen, msg, g.fontFace, op)

	// subtitle
	op2 := &text.DrawOptions{}
	op2.GeoM.Translate(screenWidth/2, screenHeight/2+50)
	op2.ColorScale.ScaleWithColor(color.RGBA{0x55, 0x66, 0x77, 0xff})
	op2.PrimaryAlign = text.AlignCenter
	text.Draw(screen, "SFO Flight Tracker", g.fontFaceSm, op2)
}

// drawHeader renders the top bar with airline info.
func (g *Game) drawHeader(screen *ebiten.Image, state tracker.State) {
	flight := state.Flight

	// Header background
	vector.DrawFilledRect(screen, 0, 0, screenWidth, 85, color.RGBA{0x0c, 0x12, 0x20, 0xff}, false)
	// Bottom accent line
	vector.DrawFilledRect(screen, 0, 83, screenWidth, 2, color.RGBA{0x00, 0x96, 0xff, 0x40}, false)

	// Airline logo
	g.drawAirlineLogo(screen, flight)

	if g.fontFaceLg == nil {
		return
	}

	// Airline name
	airlineName := g.resolveAirlineName(flight)
	xOffset := float64(85)

	op := &text.DrawOptions{}
	op.GeoM.Translate(xOffset, 12)
	op.ColorScale.ScaleWithColor(color.White)
	text.Draw(screen, airlineName, g.fontFaceLg, op)

	// Flight code + direction
	flightCode := flight.DisplayIdent()
	dirLabel := state.Direction.String()

	var cityLabel string
	if state.Direction == 0 { // Arriving
		cityLabel = fmt.Sprintf("From %s", flight.Origin.DisplayCity())
	} else {
		cityLabel = fmt.Sprintf("To %s", flight.Destination.DisplayCity())
	}

	subtitle := fmt.Sprintf("%s  ·  %s  ·  %s", flightCode, dirLabel, cityLabel)

	op2 := &text.DrawOptions{}
	op2.GeoM.Translate(xOffset, 48)
	op2.ColorScale.ScaleWithColor(color.RGBA{0x88, 0x99, 0xaa, 0xff})
	text.Draw(screen, subtitle, g.fontFaceSm, op2)

	// Status badge (top right)
	status := flight.Status
	if status == "" {
		status = "En Route"
	}
	if len(status) > 20 {
		status = status[:20]
	}

	op3 := &text.DrawOptions{}
	op3.GeoM.Translate(screenWidth-15, 20)
	op3.ColorScale.ScaleWithColor(color.RGBA{0x00, 0xc8, 0x64, 0xff})
	op3.PrimaryAlign = text.AlignEnd
	text.Draw(screen, status, g.fontFaceSm, op3)

	// Progress
	if flight.ProgressPercent != nil {
		progText := fmt.Sprintf("%d%% complete", *flight.ProgressPercent)
		op4 := &text.DrawOptions{}
		op4.GeoM.Translate(screenWidth-15, 45)
		op4.ColorScale.ScaleWithColor(color.RGBA{0x55, 0x66, 0x77, 0xff})
		op4.PrimaryAlign = text.AlignEnd
		text.Draw(screen, progText, g.fontFaceSm, op4)
	}
}

// drawMap renders the center map area.
func (g *Game) drawMap(screen *ebiten.Image, state tracker.State) {
	var lat, lon float64
	var heading *int
	if state.Position != nil {
		lat = state.Position.Latitude
		lon = state.Position.Longitude
		heading = state.Position.Heading
	}

	g.mapRender.Draw(screen, lat, lon, heading)

	// Draw SFO label on map
	if g.fontFaceSm != nil {
		sfoX, sfoY := g.mapRender.GetSFOScreenPos()
		op := &text.DrawOptions{}
		op.GeoM.Translate(float64(sfoX)+12, float64(sfoY)-8)
		op.ColorScale.ScaleWithColor(color.RGBA{0x00, 0xc8, 0xff, 0xff})
		text.Draw(screen, "SFO", g.fontFaceSm, op)
	}

	// Draw plane speed label on map
	if state.Position != nil && g.fontFaceSm != nil {
		px, py := g.mapRender.GetPlaneScreenPos(lat, lon)
		speedLabel := FormatSpeed(state.Position.Groundspeed)
		op := &text.DrawOptions{}
		op.GeoM.Translate(float64(px)+15, float64(py)-8)
		op.ColorScale.ScaleWithColor(color.RGBA{0xff, 0xc8, 0x00, 0xff})
		text.Draw(screen, speedLabel, g.fontFaceSm, op)
	}
}

// drawBottomBar renders the stats bar at the bottom.
func (g *Game) drawBottomBar(screen *ebiten.Image, state tracker.State) {
	barY := float32(screenHeight - 60)

	// Background
	vector.DrawFilledRect(screen, 0, barY, screenWidth, 60, color.RGBA{0x0c, 0x12, 0x20, 0xff}, false)
	// Top accent line
	vector.DrawFilledRect(screen, 0, barY, screenWidth, 2, color.RGBA{0x00, 0x96, 0xff, 0x40}, false)

	if state.Position == nil || g.fontFace == nil {
		return
	}

	pos := state.Position

	// Stats laid out evenly across the bar
	stats := []struct {
		label string
		value string
	}{
		{"SPEED", FormatSpeed(pos.Groundspeed)},
		{"ALTITUDE", FormatAltitude(pos.Altitude)},
		{"HEADING", FormatHeading(pos.Heading)},
	}

	// Add altitude change indicator
	altChange := ""
	switch pos.AltitudeChange {
	case "C":
		altChange = "CLIMBING"
	case "D":
		altChange = "DESCENDING"
	case "-":
		altChange = "LEVEL"
	}
	if altChange != "" {
		stats = append(stats, struct {
			label string
			value string
		}{"STATUS", altChange})
	}

	colWidth := float64(screenWidth) / float64(len(stats))

	for i, s := range stats {
		cx := colWidth*float64(i) + colWidth/2

		// Label
		op := &text.DrawOptions{}
		op.GeoM.Translate(cx, float64(barY)+10)
		op.ColorScale.ScaleWithColor(color.RGBA{0x55, 0x66, 0x77, 0xff})
		op.PrimaryAlign = text.AlignCenter
		text.Draw(screen, s.label, g.fontFaceSm, op)

		// Value
		op2 := &text.DrawOptions{}
		op2.GeoM.Translate(cx, float64(barY)+30)
		op2.ColorScale.ScaleWithColor(color.White)
		op2.PrimaryAlign = text.AlignCenter
		text.Draw(screen, s.value, g.fontFace, op2)
	}
}

// resolveAirlineName returns the full airline name for a flight.
func (g *Game) resolveAirlineName(flight *aeroapi.Flight) string {
	// Try operator ICAO first
	if flight.OperatorICAO != nil {
		if name, ok := airlineNames[*flight.OperatorICAO]; ok {
			return name
		}
	}
	// Try operator code
	if flight.Operator != nil {
		if name, ok := airlineNames[*flight.Operator]; ok {
			return name
		}
	}
	// Fallback to operator code display
	return flight.OperatorName()
}

// drawAirlineLogo draws the airline logo or a fallback.
func (g *Game) drawAirlineLogo(screen *ebiten.Image, flight *aeroapi.Flight) {
	code := flight.OperatorName()

	// Try to get cached logo
	if cached, ok := g.logoCache.Load(code); ok {
		if img, ok := cached.(*ebiten.Image); ok && img != nil {
			op := &ebiten.DrawImageOptions{}
			// Scale to 65x65 and position
			bounds := img.Bounds()
			scaleX := 65.0 / float64(bounds.Dx())
			scaleY := 65.0 / float64(bounds.Dy())
			scale := scaleX
			if scaleY < scale {
				scale = scaleY
			}
			op.GeoM.Scale(scale, scale)
			op.GeoM.Translate(10, 10)
			screen.DrawImage(img, op)
			return
		}
	}

	// Start async fetch
	go g.fetchAirlineLogo(code, flight)

	// Draw fallback rectangle with airline code
	vector.DrawFilledRect(screen, 10, 10, 65, 65, color.RGBA{0x15, 0x1f, 0x30, 0xff}, false)
	vector.StrokeRect(screen, 10, 10, 65, 65, 1, color.RGBA{0x00, 0x96, 0xff, 0x40}, false)

	if g.fontFace != nil {
		displayCode := code
		if len(displayCode) > 3 {
			displayCode = displayCode[:3]
		}
		op := &text.DrawOptions{}
		op.GeoM.Translate(42, 35)
		op.ColorScale.ScaleWithColor(color.RGBA{0x00, 0x96, 0xff, 0xff})
		op.PrimaryAlign = text.AlignCenter
		text.Draw(screen, displayCode, g.fontFace, op)
	}
}

// fetchAirlineLogo downloads and caches an airline logo.
func (g *Game) fetchAirlineLogo(code string, flight *aeroapi.Flight) {
	// Check if already fetching/fetched
	if _, loaded := g.logoCache.LoadOrStore(code, (*ebiten.Image)(nil)); loaded {
		return
	}

	// Try IATA code for logo lookup
	iataCode := ""
	if flight.OperatorIATA != nil {
		iataCode = strings.ToLower(*flight.OperatorIATA)
	}
	if iataCode == "" {
		// Map some common ICAO to IATA
		icaoToIATA := map[string]string{
			"UAL": "ua", "AAL": "aa", "DAL": "dl", "SWA": "wn", "ASA": "as",
			"JBU": "b6", "NKS": "nk", "FFT": "f9", "HAL": "ha", "SKW": "oo",
			"AFR": "af", "BAW": "ba", "DLH": "lh", "KLM": "kl", "ANA": "nh",
			"JAL": "jl", "CPA": "cx", "SIA": "sq", "QFA": "qf", "EVA": "br",
			"UAE": "ek", "ETD": "ey", "THY": "tk", "ACA": "ac",
		}
		if mapped, ok := icaoToIATA[code]; ok {
			iataCode = mapped
		} else {
			iataCode = strings.ToLower(code)
		}
	}

	// Try multiple logo sources
	urls := []string{
		fmt.Sprintf("https://content.airhex.com/content/logos/airlines_%s_50_50_s.png", iataCode),
		fmt.Sprintf("https://pics.avs.io/70/70/%s.png", strings.ToUpper(iataCode)),
	}

	for _, u := range urls {
		img := tryFetchImage(u)
		if img != nil {
			ebiImg := ebiten.NewImageFromImage(img)
			g.logoCache.Store(code, ebiImg)
			return
		}
	}

	log.Printf("[ui] could not fetch logo for %s", code)
}

// tryFetchImage attempts to download and decode an image from a URL.
func tryFetchImage(imgURL string) image.Image {
	resp, err := http.Get(imgURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	// Limit read to 512KB
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil || len(data) < 100 {
		return nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}
