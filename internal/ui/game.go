package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"github.com/subham/flighttracker/internal/provider"
	"github.com/subham/flighttracker/internal/tracker"

	_ "image/jpeg"
	_ "image/png"
)

const (
	screenWidth  = 800
	screenHeight = 480

	// Layout: 30% left panel, 70% right map
	leftPanelWidth = 240 // 30% of 800
	mapX           = 240
	mapWidth       = 560 // 70% of 800
)

// Game implements ebiten.Game for the flight tracker display.
type Game struct {
	tracker    *tracker.Tracker
	mapRender  *MapRenderer
	logoCache  sync.Map
	fontFaceSm *text.GoTextFace
	fontFace   *text.GoTextFace
	fontFaceLg *text.GoTextFace
	fontFaceXl *text.GoTextFace
}

// NewGame creates a new Game instance.
func NewGame(t *tracker.Tracker) *Game {
	g := &Game{
		tracker:   t,
		mapRender: NewMapRenderer(mapX, 0, mapWidth, screenHeight),
	}
	g.initFonts()

	// Load airplane icon
	pngData, err := os.ReadFile("internal/ui/assets/apple_sf_airplane.png")
	if err != nil {
		log.Printf("[ui] warning: could not load airplane icon: %v", err)
	} else {
		if err := g.mapRender.LoadPlaneIcon(pngData); err != nil {
			log.Printf("[ui] warning: could not decode airplane icon: %v", err)
		}
	}

	return g
}

func (g *Game) initFonts() {
	regSource, err := text.NewGoTextFaceSource(regularFontData())
	if err != nil {
		log.Printf("[ui] error loading regular font: %v", err)
		return
	}
	medSource, err := text.NewGoTextFaceSource(mediumFontData())
	if err != nil {
		medSource = regSource
	}
	sbSource, err := text.NewGoTextFaceSource(semiboldFontData())
	if err != nil {
		sbSource = medSource
	}
	boldSource, err := text.NewGoTextFaceSource(boldFontData())
	if err != nil {
		boldSource = sbSource
	}
	g.fontFaceSm = &text.GoTextFace{Source: regSource, Size: 11}
	g.fontFace = &text.GoTextFace{Source: medSource, Size: 15}
	g.fontFaceLg = &text.GoTextFace{Source: sbSource, Size: 22}
	g.fontFaceXl = &text.GoTextFace{Source: boldSource, Size: 30}
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
	screen.Fill(color.Black)

	state := g.tracker.GetState()

	if state.Flight == nil {
		g.drawWaiting(screen, state)
		return
	}

	// Right side: Map (70%)
	g.drawMap(screen, state)

	// Left side: Data panel (30%)
	g.drawLeftPanel(screen, state)

	// Divider line between panels
	vector.DrawFilledRect(screen, leftPanelWidth-1, 0, 2, screenHeight, color.RGBA{0x25, 0x25, 0x25, 0xff}, false)
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

	msg := "Searching for flights..."
	if state.Error != "" {
		msg = "Connecting..."
	}

	// Pulsing dot
	vector.DrawFilledCircle(screen, screenWidth/2, screenHeight/2-20, 8, color.RGBA{0x00, 0xbb, 0xff, 0xff}, true)

	op := &text.DrawOptions{}
	op.GeoM.Translate(screenWidth/2, screenHeight/2+20)
	op.ColorScale.ScaleWithColor(color.RGBA{0x88, 0x88, 0x88, 0xff})
	op.PrimaryAlign = text.AlignCenter
	text.Draw(screen, msg, g.fontFace, op)

	op2 := &text.DrawOptions{}
	op2.GeoM.Translate(screenWidth/2, screenHeight/2+45)
	op2.ColorScale.ScaleWithColor(color.RGBA{0x44, 0x44, 0x44, 0xff})
	op2.PrimaryAlign = text.AlignCenter
	text.Draw(screen, "SFO Flight Tracker", g.fontFaceSm, op2)
}

// drawLeftPanel renders the 30% left data panel.
func (g *Game) drawLeftPanel(screen *ebiten.Image, state tracker.State) {
	flight := state.Flight

	// Panel background — very subtle dark gray
	vector.DrawFilledRect(screen, 0, 0, leftPanelWidth, screenHeight, color.RGBA{0x0a, 0x0a, 0x0a, 0xff}, false)

	if g.fontFace == nil {
		return
	}

	// ── Airline Logo Card ──
	logoCardX := float32(12)
	logoCardY := float32(12)
	logoCardW := float32(leftPanelWidth - 24)
	logoCardH := float32(100)
	logoCardR := float32(10)

	// White rounded rectangle background
	drawRoundedRect(screen, logoCardX, logoCardY, logoCardW, logoCardH, logoCardR, color.RGBA{0xf5, 0xf5, 0xf5, 0xff})

	// Draw logo centered in card
	g.drawAirlineLogo(screen, flight, logoCardX, logoCardY, logoCardW, logoCardH)

	y := float64(logoCardY+logoCardH) + 16

	// ── Airline Name ──
	airlineName := g.resolveAirlineName(flight)
	drawText(screen, airlineName, 16, y, g.fontFaceXl, color.White)
	y += 36

	// ── Flight Code ──
	flightCode := flight.DisplayIdent()
	drawText(screen, flightCode, 16, y, g.fontFaceLg, color.RGBA{0x00, 0xbb, 0xff, 0xff})
	y += 32

	// ── Route with country flag ──
	var routeText string
	var flagAirport *provider.AirportRef
	if state.Direction == provider.Arriving && flight.Origin != nil && flight.Origin.DisplayCity() != "Unknown" {
		routeText = fmt.Sprintf("From %s", flight.Origin.DisplayCity())
		flagAirport = flight.Origin
	} else if state.Direction == provider.Departing && flight.Destination != nil && flight.Destination.DisplayCity() != "Unknown" {
		routeText = fmt.Sprintf("To %s", flight.Destination.DisplayCity())
		flagAirport = flight.Destination
	}
	if routeText != "" {
		drawText(screen, routeText, 16, y, g.fontFace, color.RGBA{0xcc, 0xcc, 0xcc, 0xff})
		// Draw country flag next to route text
		if flagAirport != nil {
			g.drawCountryFlag(screen, flagAirport, 16+textWidth(routeText, g.fontFace)+8, y-1)
		}
		y += 24
	}

	// ── Separator ──
	vector.DrawFilledRect(screen, 16, float32(y), leftPanelWidth-32, 1, color.RGBA{0x22, 0x22, 0x22, 0xff}, false)
	y += 20

	// ── Metrics ──
	if state.Position != nil {
		pos := state.Position

		metrics := []struct {
			label string
			value string
		}{
			{"SPEED", fmt.Sprintf("%d mph", int(float64(pos.Groundspeed)*1.15078))},
			{"ALTITUDE", FormatAltitude(pos.Altitude)},
			{"HEADING", FormatHeading(pos.Heading)},
		}

		for _, m := range metrics {
			// Label
			drawText(screen, m.label, 16, y, g.fontFaceSm, color.RGBA{0x55, 0x55, 0x55, 0xff})
			y += 18
			// Value
			drawText(screen, m.value, 16, y, g.fontFaceXl, color.White)
			y += 38
		}

		// ── Altitude Status ──
		y += 4
		altStatus := ""
		var altClr color.RGBA
		switch pos.AltitudeChange {
		case "C":
			altStatus = "▲ CLIMBING"
			altClr = color.RGBA{0x00, 0xcc, 0x66, 0xff}
		case "D":
			altStatus = "▼ DESCENDING"
			altClr = color.RGBA{0xff, 0x66, 0x44, 0xff}
		case "-":
			altStatus = "━ LEVEL"
			altClr = color.RGBA{0xaa, 0xaa, 0xaa, 0xff}
		}
		if altStatus != "" {
			drawText(screen, altStatus, 16, y, g.fontFaceLg, altClr)
		}
	}
}

// drawMap renders the right-side map.
func (g *Game) drawMap(screen *ebiten.Image, state tracker.State) {
	var lat, lon float64
	var heading *int
	if state.Position != nil {
		lat = state.Position.Latitude
		lon = state.Position.Longitude
		heading = state.Position.Heading
	}

	g.mapRender.Draw(screen, lat, lon, heading)

	// SFO label
	if g.fontFaceSm != nil {
		sfoX, sfoY := g.mapRender.GetSFOScreenPos()
		if sfoX >= mapX && sfoX <= screenWidth {
			op := &text.DrawOptions{}
			op.GeoM.Translate(float64(sfoX)+12, float64(sfoY)-6)
			op.ColorScale.ScaleWithColor(color.RGBA{0x00, 0xdd, 0xff, 0xff})
			text.Draw(screen, "SFO", g.fontFaceSm, op)
		}
	}

	// Plane label — just show ident
	if state.Position != nil && g.fontFaceSm != nil && state.Flight != nil {
		px, py := g.mapRender.GetPlaneScreenPos(lat, lon)
		if px >= mapX && px <= screenWidth {
			op := &text.DrawOptions{}
			op.GeoM.Translate(float64(px)+22, float64(py)-6)
			op.ColorScale.ScaleWithColor(color.White)
			text.Draw(screen, state.Flight.DisplayIdent(), g.fontFaceSm, op)
		}
	}
}

// resolveAirlineName returns the full airline name using the airlines.json dataset.
func (g *Game) resolveAirlineName(flight *provider.Flight) string {
	// Best: lookup by IATA code
	if flight.OperatorIATA != "" {
		if a, ok := LookupAirlineByIATA(flight.OperatorIATA); ok {
			return a.Name
		}
	}
	// Fallback: lookup by operator name
	if flight.Operator != "" {
		if a, ok := LookupAirlineByName(flight.Operator); ok {
			return a.Name
		}
	}
	return flight.OperatorName()
}

// drawAirlineLogo draws the airline logo centered inside the white card area.
func (g *Game) drawAirlineLogo(screen *ebiten.Image, flight *provider.Flight, cardX, cardY, cardW, cardH float32) {
	code := flight.OperatorName()

	maxLogoW := float64(cardW) * 0.90
	maxLogoH := float64(cardH) * 0.80

	if cached, ok := g.logoCache.Load(code); ok {
		if img, ok := cached.(*ebiten.Image); ok && img != nil {
			op := &ebiten.DrawImageOptions{}
			bounds := img.Bounds()
			scaleX := maxLogoW / float64(bounds.Dx())
			scaleY := maxLogoH / float64(bounds.Dy())
			scale := scaleX
			if scaleY < scale {
				scale = scaleY
			}
			scaledW := float64(bounds.Dx()) * scale
			scaledH := float64(bounds.Dy()) * scale
			// Center in card
			offX := float64(cardX) + (float64(cardW)-scaledW)/2
			offY := float64(cardY) + (float64(cardH)-scaledH)/2
			op.GeoM.Scale(scale, scale)
			op.GeoM.Translate(offX, offY)
			screen.DrawImage(img, op)
			return
		}
	}

	go g.fetchAirlineLogo(code, flight)

	// Fallback: show airline code centered in card
	if g.fontFaceLg != nil {
		displayCode := code
		if len(displayCode) > 4 {
			displayCode = displayCode[:4]
		}
		op := &text.DrawOptions{}
		op.GeoM.Translate(float64(cardX+cardW/2), float64(cardY+cardH/2)-11)
		op.ColorScale.ScaleWithColor(color.RGBA{0x88, 0x88, 0x88, 0xff})
		op.PrimaryAlign = text.AlignCenter
		text.Draw(screen, displayCode, g.fontFaceLg, op)
	}
}

// drawRoundedRect draws a filled rounded rectangle.
func drawRoundedRect(screen *ebiten.Image, x, y, w, h, r float32, clr color.Color) {
	// Center fill
	vector.DrawFilledRect(screen, x+r, y, w-2*r, h, clr, true)
	// Left fill
	vector.DrawFilledRect(screen, x, y+r, r, h-2*r, clr, true)
	// Right fill
	vector.DrawFilledRect(screen, x+w-r, y+r, r, h-2*r, clr, true)
	// Four corners
	vector.DrawFilledCircle(screen, x+r, y+r, r, clr, true)
	vector.DrawFilledCircle(screen, x+w-r, y+r, r, clr, true)
	vector.DrawFilledCircle(screen, x+r, y+h-r, r, clr, true)
	vector.DrawFilledCircle(screen, x+w-r, y+h-r, r, clr, true)
}

func (g *Game) fetchAirlineLogo(code string, flight *provider.Flight) {
	if _, loaded := g.logoCache.LoadOrStore(code, (*ebiten.Image)(nil)); loaded {
		return
	}

	iataCode := ""
	if flight.OperatorIATA != "" {
		iataCode = strings.ToLower(flight.OperatorIATA)
	}
	if iataCode == "" {
		iataCode = strings.ToLower(code)
	}

	// Fetch clean airline logos from external services
	urls := []string{
		fmt.Sprintf("https://content.airhex.com/content/logos/airlines_%s_350_350_s.png", iataCode),
		fmt.Sprintf("https://pics.avs.io/350/350/%s.png", strings.ToUpper(iataCode)),
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

func tryFetchImage(imgURL string) image.Image {
	resp, err := http.Get(imgURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

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

// drawText is a helper to draw text at a given position.
func drawText(screen *ebiten.Image, s string, x, y float64, face *text.GoTextFace, clr color.Color) {
	if face == nil {
		return
	}
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(clr)
	text.Draw(screen, s, face, op)
}

// textWidth measures the pixel width of a string with the given font face.
func textWidth(s string, face *text.GoTextFace) float64 {
	if face == nil {
		return 0
	}
	w, _ := text.Measure(s, face, 0)
	return w
}

// drawCountryFlag draws a small country flag image next to the route text.
func (g *Game) drawCountryFlag(screen *ebiten.Image, airport *provider.AirportRef, x, y float64) {
	countryCode := icaoToCountryCode(airport)
	if countryCode == "" {
		return
	}

	cacheKey := "flag_" + countryCode
	if cached, ok := g.logoCache.Load(cacheKey); ok {
		if img, ok := cached.(*ebiten.Image); ok && img != nil {
			op := &ebiten.DrawImageOptions{}
			bounds := img.Bounds()
			// Scale to 22x16
			scaleX := 22.0 / float64(bounds.Dx())
			scaleY := 16.0 / float64(bounds.Dy())
			op.GeoM.Scale(scaleX, scaleY)
			op.GeoM.Translate(x, y+1)
			screen.DrawImage(img, op)
		}
		return
	}

	// Fetch async
	go func() {
		if _, loaded := g.logoCache.LoadOrStore(cacheKey, (*ebiten.Image)(nil)); loaded {
			return
		}
		url := fmt.Sprintf("https://flagcdn.com/w80/%s.png", countryCode)
		img := tryFetchImage(url)
		if img != nil {
			ebiImg := ebiten.NewImageFromImage(img)
			g.logoCache.Store(cacheKey, ebiImg)
		}
	}()
}

// icaoToCountryCode extracts the ISO 3166-1 alpha-2 country code from an airport's ICAO code.
func icaoToCountryCode(airport *provider.AirportRef) string {
	code := ""
	if airport.CodeICAO != "" {
		code = airport.CodeICAO
	} else if airport.Code != "" {
		code = airport.Code
	}
	if len(code) < 2 {
		return ""
	}

	prefix2 := code[:2]
	prefix1 := code[:1]

	// 2-letter prefix matches (more specific)
	if cc, ok := icaoPrefixToCountry[prefix2]; ok {
		return cc
	}
	// 1-letter prefix matches
	if cc, ok := icaoPrefixToCountry[prefix1]; ok {
		return cc
	}
	return ""
}

// icaoPrefixToCountry maps ICAO airport code prefixes to ISO 3166-1 alpha-2 country codes.
var icaoPrefixToCountry = map[string]string{
	// North America
	"K":  "us", // USA (continental)
	"PH": "us", // Hawaii
	"PA": "us", // Alaska
	"PG": "us", // Guam
	"C":  "ca", // Canada
	"MM": "mx", // Mexico

	// Central America & Caribbean
	"MG": "gt", // Guatemala
	"MH": "hn", // Honduras
	"MN": "ni", // Nicaragua
	"MR": "cr", // Costa Rica
	"MP": "pa", // Panama
	"MK": "jm", // Jamaica
	"MT": "ht", // Haiti
	"MD": "do", // Dominican Republic
	"MU": "cu", // Cuba
	"TB": "bb", // Barbados
	"TT": "tt", // Trinidad
	"TJ": "pr", // Puerto Rico
	"TI": "vi", // U.S. Virgin Islands

	// South America
	"SB": "br", // Brazil
	"SA": "ar", // Argentina
	"SC": "cl", // Chile
	"SK": "co", // Colombia
	"SP": "pe", // Peru
	"SV": "ve", // Venezuela
	"SE": "ec", // Ecuador
	"SU": "uy", // Uruguay
	"SG": "py", // Paraguay
	"SL": "bo", // Bolivia

	// Europe
	"EG": "gb", // United Kingdom
	"EI": "ie", // Ireland
	"LF": "fr", // France
	"ED": "de", // Germany
	"LI": "it", // Italy
	"LE": "es", // Spain
	"LP": "pt", // Portugal
	"EH": "nl", // Netherlands
	"EB": "be", // Belgium
	"LS": "ch", // Switzerland
	"LO": "at", // Austria
	"EK": "dk", // Denmark
	"EN": "no", // Norway
	"ES": "se", // Sweden
	"EF": "fi", // Finland
	"EE": "ee", // Estonia
	"EV": "lv", // Latvia
	"EY": "lt", // Lithuania
	"EP": "pl", // Poland
	"LK": "cz", // Czech Republic
	"LZ": "sk", // Slovakia
	"LH": "hu", // Hungary
	"LR": "ro", // Romania
	"LB": "bg", // Bulgaria
	"LG": "gr", // Greece
	"LT": "tr", // Turkey
	"LJ": "si", // Slovenia
	"LD": "hr", // Croatia
	"LY": "rs", // Serbia
	"BI": "is", // Iceland
	"LU": "md", // Moldova
	"UK": "ua", // Ukraine

	// Middle East
	"OE": "sa", // Saudi Arabia
	"OM": "ae", // UAE
	"OB": "bh", // Bahrain
	"OK": "kw", // Kuwait
	"OO": "om", // Oman
	"OT": "qa", // Qatar
	"OI": "ir", // Iran
	"OJ": "jo", // Jordan
	"OL": "lb", // Lebanon
	"OS": "sy", // Syria
	"LL": "il", // Israel

	// Asia
	"ZS": "cn", // China (south)
	"ZB": "cn", // China (north)
	"ZG": "cn", // China (central)
	"ZU": "cn", // China (west)
	"ZW": "cn", // China
	"ZH": "cn", // China
	"RJ": "jp", // Japan
	"RK": "kr", // South Korea
	"VT": "th", // Thailand
	"WS": "sg", // Singapore
	"WM": "my", // Malaysia
	"WI": "id", // Indonesia
	"WA": "id", // Indonesia
	"RP": "ph", // Philippines
	"VV": "vn", // Vietnam
	"VH": "hk", // Hong Kong
	"VM": "mo", // Macau
	"RC": "tw", // Taiwan
	"VI": "in", // India (north)
	"VO": "in", // India (south)
	"VA": "in", // India (west)
	"VE": "in", // India (east)
	"VQ": "bt", // Bhutan
	"VN": "np", // Nepal
	"VL": "la", // Laos
	"VY": "mm", // Myanmar
	"VC": "lk", // Sri Lanka
	"OP": "pk", // Pakistan

	// Oceania
	"Y":  "au", // Australia
	"NZ": "nz", // New Zealand
	"NF": "fj", // Fiji
	"PF": "us", // Midway
	"PT": "fm", // Micronesia

	// Africa
	"DA": "dz", // Algeria
	"DT": "tn", // Tunisia
	"GM": "ma", // Morocco
	"HA": "et", // Ethiopia
	"HK": "ke", // Kenya
	"HT": "tz", // Tanzania
	"HR": "rw", // Rwanda
	"HU": "ug", // Uganda
	"FA": "za", // South Africa
	"FV": "zw", // Zimbabwe
	"DN": "ng", // Nigeria
	"DG": "gh", // Ghana
	"FW": "mw", // Malawi
	"FL": "zm", // Zambia
	"HE": "eg", // Egypt
	"HC": "so", // Somalia

	// Russia
	"U": "ru", // Russia
}
