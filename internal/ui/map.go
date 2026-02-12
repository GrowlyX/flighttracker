package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"net/http"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	_ "image/jpeg"
	_ "image/png"
)

const (
	// SFO coordinates
	sfoLat = 37.6213
	sfoLon = -122.3790

	tileSize   = 256
	maxZoom    = 18
	minZoom    = 2
	tileMaxAge = 500 // max cached tiles
)

// TileKey uniquely identifies a map tile.
type TileKey struct {
	Z, X, Y int
}

// MapRenderer draws an OpenStreetMap tile-based map with flight position.
type MapRenderer struct {
	// Screen region for the map
	x, y, w, h float32

	// Map state
	centerLat, centerLon             float64
	targetCenterLat, targetCenterLon float64
	zoom                             float64
	targetZoom                       float64

	// Tile cache
	tileCache sync.Map // map[TileKey]*ebiten.Image
	tileFetch sync.Map // map[TileKey]bool (in-flight fetches)
	fetchSem  chan struct{}

	// Plane icon
	planeImg *ebiten.Image
}

// NewMapRenderer creates a map renderer for the given screen region.
func NewMapRenderer(x, y, w, h float32) *MapRenderer {
	return &MapRenderer{
		x:          x,
		y:          y,
		w:          w,
		h:          h,
		centerLat:  sfoLat,
		centerLon:  sfoLon,
		zoom:       12,
		targetZoom: 12,
		fetchSem:   make(chan struct{}, 4), // max 4 concurrent tile fetches
	}
}

// latLonToTileXY converts lat/lon to tile coordinates at a given zoom level.
func latLonToTileXY(lat, lon float64, zoom int) (float64, float64) {
	n := math.Pow(2, float64(zoom))
	x := (lon + 180.0) / 360.0 * n
	latRad := lat * math.Pi / 180.0
	y := (1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) / 2.0 * n
	return x, y
}

// tileXYToLatLon converts tile coordinates back to lat/lon.
func tileXYToLatLon(x, y float64, zoom int) (float64, float64) {
	n := math.Pow(2, float64(zoom))
	lon := x/n*360.0 - 180.0
	latRad := math.Atan(math.Sinh(math.Pi * (1.0 - 2.0*y/n)))
	lat := latRad * 180.0 / math.Pi
	return lat, lon
}

// Update recalculates the map viewport to fit both SFO and the plane.
func (m *MapRenderer) Update(planeLat, planeLon float64) {
	if planeLat == 0 && planeLon == 0 {
		m.targetCenterLat = sfoLat
		m.targetCenterLon = sfoLon
		m.targetZoom = 12
	} else {
		// Center between SFO and plane
		m.targetCenterLat = (sfoLat + planeLat) / 2
		m.targetCenterLon = (sfoLon + planeLon) / 2

		// Continuous zoom formula
		latDiff := math.Abs(sfoLat - planeLat)
		lonDiff := math.Abs(sfoLon - planeLon)
		maxDiff := math.Max(latDiff, lonDiff)

		if maxDiff < 0.02 {
			maxDiff = 0.02 // floor to prevent extreme zoom-in
		}

		// zoom ≈ 8.5 - 3.0 * log2(maxDiff)
		newZoom := 8.5 - 3.0*math.Log2(maxDiff)
		newZoom = math.Max(3, math.Min(12, newZoom)) // cap at 12 (was 15)

		m.targetZoom = newZoom
	}

	// Smooth center panning
	centerBlend := 0.06
	m.centerLat += (m.targetCenterLat - m.centerLat) * centerBlend
	m.centerLon += (m.targetCenterLon - m.centerLon) * centerBlend

	// Rate-limited smooth zoom transition
	diff := m.targetZoom - m.zoom
	maxStep := 0.05
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	m.zoom += diff
}

// latLonToScreen converts geographic coordinates to screen pixel coordinates.
func (m *MapRenderer) latLonToScreen(lat, lon float64) (float32, float32) {
	z := int(math.Round(m.zoom))
	// Center tile coordinates
	cx, cy := latLonToTileXY(m.centerLat, m.centerLon, z)
	// Point tile coordinates
	px, py := latLonToTileXY(lat, lon, z)

	// Pixel offset from center
	dx := (px - cx) * tileSize
	dy := (py - cy) * tileSize

	sx := m.x + m.w/2 + float32(dx)
	sy := m.y + m.h/2 + float32(dy)

	return sx, sy
}

// Draw renders the tile map onto the screen.
func (m *MapRenderer) Draw(screen *ebiten.Image, planeLat, planeLon float64, heading *int, trail [][2]float64) {
	// Black background for the map area
	vector.DrawFilledRect(screen, m.x, m.y, m.w, m.h, color.Black, false)

	z := int(math.Round(m.zoom))

	// Calculate which tiles we need
	cx, cy := latLonToTileXY(m.centerLat, m.centerLon, z)

	// How many tiles fit on screen
	tilesW := int(math.Ceil(float64(m.w)/tileSize)) + 2
	tilesH := int(math.Ceil(float64(m.h)/tileSize)) + 2

	centerTileX := int(math.Floor(cx))
	centerTileY := int(math.Floor(cy))

	// Offset of center tile's top-left corner relative to screen center
	offsetX := float64(m.x) + float64(m.w)/2 - (cx-math.Floor(cx))*tileSize
	offsetY := float64(m.y) + float64(m.h)/2 - (cy-math.Floor(cy))*tileSize

	maxTile := int(math.Pow(2, float64(z)))

	// Draw tiles
	for dy := -tilesH / 2; dy <= tilesH/2; dy++ {
		for dx := -tilesW / 2; dx <= tilesW/2; dx++ {
			tileX := centerTileX + dx
			tileY := centerTileY + dy

			// Wrap X around the world
			if tileX < 0 {
				tileX += maxTile
			} else if tileX >= maxTile {
				tileX -= maxTile
			}
			// Skip invalid Y
			if tileY < 0 || tileY >= maxTile {
				continue
			}

			key := TileKey{Z: z, X: tileX, Y: tileY}
			tile := m.getTile(key)

			screenX := offsetX + float64(dx)*tileSize
			screenY := offsetY + float64(dy)*tileSize

			if tile != nil {
				op := &ebiten.DrawImageOptions{}
				op.GeoM.Translate(screenX, screenY)
				// Darken the tiles for a dark theme
				op.ColorScale.Scale(0.4, 0.4, 0.45, 1.0)
				screen.DrawImage(tile, op)
			}
		}
	}

	// Clip: draw black borders outside the map area
	// Top
	if m.y > 0 {
		vector.DrawFilledRect(screen, m.x, 0, m.w, m.y, color.Black, false)
	}

	// Draw trail from recorded positions (replaces straight line)
	if len(trail) > 1 {
		m.drawTrail(screen, trail)
	} else if planeLat != 0 || planeLon != 0 {
		// Fallback: straight line if no trail yet
		m.drawRouteLine(screen, planeLat, planeLon)
	}

	// Draw SFO marker
	m.drawAirportMarker(screen, sfoLat, sfoLon)

	// Draw plane
	if planeLat != 0 || planeLon != 0 {
		m.drawPlane(screen, planeLat, planeLon, heading)
	}
}

// getTile returns a cached tile image, or starts an async fetch.
func (m *MapRenderer) getTile(key TileKey) *ebiten.Image {
	if cached, ok := m.tileCache.Load(key); ok {
		return cached.(*ebiten.Image)
	}

	// Start async fetch if not already in progress
	if _, fetching := m.tileFetch.LoadOrStore(key, true); !fetching {
		go m.fetchTile(key)
	}
	return nil
}

// fetchTile downloads a tile from OpenStreetMap.
func (m *MapRenderer) fetchTile(key TileKey) {
	// Semaphore to limit concurrent fetches
	m.fetchSem <- struct{}{}
	defer func() { <-m.fetchSem }()

	url := fmt.Sprintf("https://tile.openstreetmap.org/%d/%d/%d.png", key.Z, key.X, key.Y)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		m.tileFetch.Delete(key)
		return
	}
	req.Header.Set("User-Agent", "SFOFlightTracker/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		m.tileFetch.Delete(key)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		m.tileFetch.Delete(key)
		return
	}

	img, _, err := image.Decode(byteReader(data))
	if err != nil {
		m.tileFetch.Delete(key)
		return
	}

	ebiImg := ebiten.NewImageFromImage(img)
	m.tileCache.Store(key, ebiImg)
}

// drawTrail draws the actual recorded flight path as a polyline with fading tail.
func (m *MapRenderer) drawTrail(screen *ebiten.Image, trail [][2]float64) {
	n := len(trail)
	if n < 2 {
		return
	}

	for i := 0; i < n-1; i++ {
		x1, y1 := m.latLonToScreen(trail[i][0], trail[i][1])
		x2, y2 := m.latLonToScreen(trail[i+1][0], trail[i+1][1])

		// Skip segments that wrap or are outside visible area
		if math.Abs(float64(x2-x1)) > float64(m.w)/2 {
			continue
		}

		// Fade opacity: older points are more transparent
		progress := float64(i) / float64(n-1)  // 0 = oldest, 1 = newest
		alpha := uint8(40 + int(progress*180)) // 40..220
		lineColor := color.RGBA{0x00, 0xcc, 0xff, alpha}

		// Thicker near current position
		width := float32(1.5 + progress*1.5) // 1.5..3.0

		vector.StrokeLine(screen, x1, y1, x2, y2, width, lineColor, true)
	}
}

// drawRouteLine draws a great circle arc from SFO to the plane.
func (m *MapRenderer) drawRouteLine(screen *ebiten.Image, lat, lon float64) {
	// Generate points along the great circle path using SLERP.
	const numSegments = 64
	points := greatCirclePoints(sfoLat, sfoLon, lat, lon, numSegments)

	routeColor := color.RGBA{0x00, 0xbb, 0xff, 0x90}

	for i := 0; i < len(points)-1; i++ {
		x1, y1 := m.latLonToScreen(points[i][0], points[i][1])
		x2, y2 := m.latLonToScreen(points[i+1][0], points[i+1][1])

		// Skip segments that wrap around the screen (antimeridian crossing artifact)
		if math.Abs(float64(x2-x1)) > float64(m.w)/2 {
			continue
		}

		vector.StrokeLine(screen, x1, y1, x2, y2, 2.5, routeColor, true)
	}
}

// greatCirclePoints computes intermediate lat/lon points along a great circle arc
// using spherical linear interpolation (SLERP).
func greatCirclePoints(lat1, lon1, lat2, lon2 float64, n int) [][2]float64 {
	φ1 := lat1 * math.Pi / 180
	λ1 := lon1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	λ2 := lon2 * math.Pi / 180

	// Central angle using the Vincenty formula (more stable for small/large angles)
	dλ := λ2 - λ1
	Δσ := math.Atan2(
		math.Sqrt(math.Pow(math.Cos(φ2)*math.Sin(dλ), 2)+
			math.Pow(math.Cos(φ1)*math.Sin(φ2)-math.Sin(φ1)*math.Cos(φ2)*math.Cos(dλ), 2)),
		math.Sin(φ1)*math.Sin(φ2)+math.Cos(φ1)*math.Cos(φ2)*math.Cos(dλ),
	)

	if Δσ < 1e-10 {
		// Points are essentially the same location
		return [][2]float64{{lat1, lon1}, {lat2, lon2}}
	}

	points := make([][2]float64, n+1)
	for i := 0; i <= n; i++ {
		f := float64(i) / float64(n)

		a := math.Sin((1-f)*Δσ) / math.Sin(Δσ)
		b := math.Sin(f*Δσ) / math.Sin(Δσ)

		x := a*math.Cos(φ1)*math.Cos(λ1) + b*math.Cos(φ2)*math.Cos(λ2)
		y := a*math.Cos(φ1)*math.Sin(λ1) + b*math.Cos(φ2)*math.Sin(λ2)
		z := a*math.Sin(φ1) + b*math.Sin(φ2)

		φ := math.Atan2(z, math.Sqrt(x*x+y*y))
		λ := math.Atan2(y, x)

		points[i] = [2]float64{φ * 180 / math.Pi, λ * 180 / math.Pi}
	}
	return points
}

// drawAirportMarker draws SFO dot.
func (m *MapRenderer) drawAirportMarker(screen *ebiten.Image, lat, lon float64) {
	x, y := m.latLonToScreen(lat, lon)

	// Outer glow
	vector.DrawFilledCircle(screen, x, y, 10, color.RGBA{0x00, 0xbb, 0xff, 0x30}, true)
	// Ring
	vector.StrokeCircle(screen, x, y, 6, 1.5, color.RGBA{0x00, 0xdd, 0xff, 0xcc}, true)
	// Inner dot
	vector.DrawFilledCircle(screen, x, y, 3, color.RGBA{0x00, 0xdd, 0xff, 0xff}, true)
}

// drawPlane draws the aircraft icon at the given position using the airplane PNG.
func (m *MapRenderer) drawPlane(screen *ebiten.Image, lat, lon float64, heading *int) {
	if m.planeImg == nil {
		return
	}

	x, y := m.latLonToScreen(lat, lon)

	angle := 0.0
	if heading != nil {
		// The airplane PNG points up-right (~45°). Subtract 45° so heading=0 means north.
		angle = (float64(*heading) - 45) * math.Pi / 180
	}

	bounds := m.planeImg.Bounds()
	imgW := float64(bounds.Dx())
	imgH := float64(bounds.Dy())

	// Scale to ~36px on screen
	targetSize := 36.0
	scale := targetSize / math.Max(imgW, imgH)

	op := &ebiten.DrawImageOptions{}

	// Move origin to center of image for rotation
	op.GeoM.Translate(-imgW/2, -imgH/2)
	op.GeoM.Scale(scale, scale)
	op.GeoM.Rotate(angle)
	op.GeoM.Translate(float64(x), float64(y))

	screen.DrawImage(m.planeImg, op)
}

// LoadPlaneIcon loads the airplane icon image from raw PNG bytes.
func (m *MapRenderer) LoadPlaneIcon(pngData []byte) error {
	img, _, err := image.Decode(bytes.NewReader(pngData))
	if err != nil {
		return fmt.Errorf("decode plane icon: %w", err)
	}
	m.planeImg = ebiten.NewImageFromImage(img)
	return nil
}

// GetSFOScreenPos returns the screen position of SFO (for label rendering).
func (m *MapRenderer) GetSFOScreenPos() (float32, float32) {
	return m.latLonToScreen(sfoLat, sfoLon)
}

// GetPlaneScreenPos returns the screen position of the plane.
func (m *MapRenderer) GetPlaneScreenPos(lat, lon float64) (float32, float32) {
	return m.latLonToScreen(lat, lon)
}

// FormatSpeed returns a formatted speed string.
func FormatSpeed(knots int) string {
	mph := int(float64(knots) * 1.15078)
	return fmt.Sprintf("%d mph", mph)
}

// FormatAltitude returns a formatted altitude string.
func FormatAltitude(hundredsFeet int) string {
	feet := hundredsFeet * 100
	if feet >= 10000 {
		return fmt.Sprintf("%d,%03d ft", feet/1000, feet%1000)
	}
	return fmt.Sprintf("%d ft", feet)
}

// FormatHeading returns a formatted heading with cardinal direction.
func FormatHeading(deg *int) string {
	if deg == nil {
		return "---°"
	}
	directions := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	idx := ((*deg + 22) / 45) % 8
	return fmt.Sprintf("%d° %s", *deg, directions[idx])
}

// byteReader wraps raw bytes for image decoding.
func byteReader(data []byte) *byteReaderImpl {
	return &byteReaderImpl{data: data, pos: 0}
}

type byteReaderImpl struct {
	data []byte
	pos  int
}

func (r *byteReaderImpl) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
