package ui

import (
	"fmt"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

const (
	// SFO coordinates
	sfoLat = 37.6213
	sfoLon = -122.3790
)

// MapRenderer draws a simplified vector map with flight position relative to SFO.
type MapRenderer struct {
	// Map viewport in lat/lon
	centerLat, centerLon float64
	zoomLevel            float64

	// Screen region for the map
	x, y, w, h float32

	// Coastline data (simplified California coast)
	coastline [][]float64
}

// NewMapRenderer creates a map renderer for the given screen region.
func NewMapRenderer(x, y, w, h float32) *MapRenderer {
	m := &MapRenderer{
		x:         x,
		y:         y,
		w:         w,
		h:         h,
		centerLat: sfoLat,
		centerLon: sfoLon,
		zoomLevel: 4.0,
	}
	m.initCoastline()
	return m
}

// initCoastline sets up simplified California/US West Coast coastline points.
func (m *MapRenderer) initCoastline() {
	m.coastline = [][]float64{
		// Pacific Coast - simplified polygon points [lat, lon]
		// Washington
		{48.39, -124.73}, {48.20, -123.50}, {47.75, -124.50},
		{47.00, -124.40}, {46.27, -124.10},
		// Oregon
		{46.00, -123.95}, {45.00, -124.00}, {44.00, -124.10},
		{43.00, -124.35}, {42.00, -124.40},
		// Northern California
		{41.99, -124.20}, {41.00, -124.10}, {40.44, -124.40},
		{40.00, -124.10}, {39.00, -123.80}, {38.50, -123.30},
		{38.00, -123.00}, {37.80, -122.52}, {37.79, -122.51},
		// San Francisco Bay entrance
		{37.81, -122.48}, {37.83, -122.47}, {37.85, -122.48},
		// North of Golden Gate
		{37.90, -122.60}, {38.00, -122.95},
		// Back to SF Peninsula
		{37.78, -122.51}, {37.70, -122.50}, {37.60, -122.38},
		{37.50, -122.35}, {37.30, -122.40},
		// Monterey Bay
		{37.00, -122.15}, {36.97, -122.03}, {36.80, -121.80},
		{36.60, -121.90}, {36.55, -121.95},
		// Big Sur
		{36.00, -121.55}, {35.50, -121.00},
		// Central California
		{35.00, -120.65}, {34.70, -120.60}, {34.45, -120.00},
		// Santa Barbara
		{34.40, -119.70}, {34.00, -118.80},
		// Los Angeles
		{33.95, -118.50}, {33.75, -118.40}, {33.70, -118.30},
		// San Diego
		{33.20, -117.40}, {32.70, -117.25}, {32.53, -117.13},
	}
}

// Update recalculates the map viewport to fit both SFO and the plane.
func (m *MapRenderer) Update(planeLat, planeLon float64) {
	if planeLat == 0 && planeLon == 0 {
		m.centerLat = sfoLat
		m.centerLon = sfoLon
		m.zoomLevel = 4.0
		return
	}

	// Center between SFO and plane
	m.centerLat = (sfoLat + planeLat) / 2
	m.centerLon = (sfoLon + planeLon) / 2

	// Auto-zoom to fit both points with padding
	latDiff := math.Abs(sfoLat - planeLat)
	lonDiff := math.Abs(sfoLon - planeLon)
	maxDiff := math.Max(latDiff, lonDiff)
	if maxDiff < 1 {
		maxDiff = 1
	}
	m.zoomLevel = maxDiff * 1.5
}

// latLonToScreen converts geographic coordinates to screen pixel coordinates.
func (m *MapRenderer) latLonToScreen(lat, lon float64) (float32, float32) {
	// Simple Mercator-like projection
	lonRange := m.zoomLevel * float64(m.w) / float64(m.h)
	latRange := m.zoomLevel

	relLon := (lon - m.centerLon) / lonRange
	relLat := (m.centerLat - lat) / latRange // Inverted: north is up

	sx := m.x + m.w/2 + float32(relLon)*m.w
	sy := m.y + m.h/2 + float32(relLat)*m.h

	return sx, sy
}

// Draw renders the map onto the screen image.
func (m *MapRenderer) Draw(screen *ebiten.Image, planeLat, planeLon float64, heading *int) {
	// Background - dark ocean
	vector.DrawFilledRect(screen, m.x, m.y, m.w, m.h, color.RGBA{0x0a, 0x0f, 0x1a, 0xff}, false)

	// Draw coastline
	m.drawCoastline(screen)

	// Draw route line from SFO to plane
	if planeLat != 0 || planeLon != 0 {
		m.drawRouteLine(screen, planeLat, planeLon)
	}

	// Draw SFO marker
	m.drawAirportMarker(screen, sfoLat, sfoLon, "SFO")

	// Draw plane
	if planeLat != 0 || planeLon != 0 {
		m.drawPlane(screen, planeLat, planeLon, heading)
	}
}

// drawCoastline renders the simplified coastline.
func (m *MapRenderer) drawCoastline(screen *ebiten.Image) {
	coastColor := color.RGBA{0x1a, 0x2a, 0x3a, 0xff}

	for i := 0; i < len(m.coastline)-1; i++ {
		x1, y1 := m.latLonToScreen(m.coastline[i][0], m.coastline[i][1])
		x2, y2 := m.latLonToScreen(m.coastline[i+1][0], m.coastline[i+1][1])

		// Only draw if at least partially visible
		if (x1 >= m.x-50 && x1 <= m.x+m.w+50) || (x2 >= m.x-50 && x2 <= m.x+m.w+50) {
			vector.StrokeLine(screen, x1, y1, x2, y2, 2, coastColor, false)
		}
	}

	// Fill the land side (simplified - draw thicker coast lines)
	landColor := color.RGBA{0x12, 0x1e, 0x2e, 0xff}
	for i := 0; i < len(m.coastline)-1; i++ {
		x1, y1 := m.latLonToScreen(m.coastline[i][0], m.coastline[i][1])
		x2, y2 := m.latLonToScreen(m.coastline[i+1][0], m.coastline[i+1][1])
		if (x1 >= m.x-50 && x1 <= m.x+m.w+50) || (x2 >= m.x-50 && x2 <= m.x+m.w+50) {
			vector.StrokeLine(screen, x1+1, y1, x2+1, y2, 4, landColor, false)
		}
	}
}

// drawRouteLine draws a dashed line from SFO to the plane.
func (m *MapRenderer) drawRouteLine(screen *ebiten.Image, lat, lon float64) {
	sfoX, sfoY := m.latLonToScreen(sfoLat, sfoLon)
	planeX, planeY := m.latLonToScreen(lat, lon)

	routeColor := color.RGBA{0x00, 0x96, 0xff, 0x80}
	vector.StrokeLine(screen, sfoX, sfoY, planeX, planeY, 2, routeColor, false)
}

// drawAirportMarker draws a labeled airport dot.
func (m *MapRenderer) drawAirportMarker(screen *ebiten.Image, lat, lon float64, label string) {
	x, y := m.latLonToScreen(lat, lon)

	// Outer glow
	vector.DrawFilledCircle(screen, x, y, 8, color.RGBA{0x00, 0x96, 0xff, 0x40}, false)
	// Inner dot
	vector.DrawFilledCircle(screen, x, y, 4, color.RGBA{0x00, 0xc8, 0xff, 0xff}, false)
	// Center
	vector.DrawFilledCircle(screen, x, y, 2, color.RGBA{0xff, 0xff, 0xff, 0xff}, false)

	// Label drawn by the game (needs font face)
	_ = label
}

// drawPlane draws the aircraft icon at the given position.
func (m *MapRenderer) drawPlane(screen *ebiten.Image, lat, lon float64, heading *int) {
	x, y := m.latLonToScreen(lat, lon)

	// Plane triangle
	angle := 0.0
	if heading != nil {
		angle = float64(*heading) * math.Pi / 180
	}

	size := float32(12)

	// Nose
	nx := x + float32(math.Sin(angle))*size
	ny := y - float32(math.Cos(angle))*size

	// Left wing
	leftAngle := angle - 2.5
	lx := x + float32(math.Sin(leftAngle))*size*0.6
	ly := y - float32(math.Cos(leftAngle))*size*0.6

	// Right wing
	rightAngle := angle + 2.5
	rx := x + float32(math.Sin(rightAngle))*size*0.6
	ry := y - float32(math.Cos(rightAngle))*size*0.6

	// Draw triangle
	planeColor := color.RGBA{0xff, 0xc8, 0x00, 0xff}

	var path vector.Path
	path.MoveTo(nx, ny)
	path.LineTo(lx, ly)
	path.LineTo(rx, ry)
	path.Close()

	vs, is := path.AppendVerticesAndIndicesForFilling(nil, nil)
	for i := range vs {
		vs[i].SrcX = 1
		vs[i].SrcY = 1
		vs[i].ColorR = float32(planeColor.R) / 255
		vs[i].ColorG = float32(planeColor.G) / 255
		vs[i].ColorB = float32(planeColor.B) / 255
		vs[i].ColorA = float32(planeColor.A) / 255
	}

	op := &ebiten.DrawTrianglesOptions{}
	op.AntiAlias = true
	screen.DrawTriangles(vs, is, emptySubImage(), op)

	// Glow around plane
	vector.DrawFilledCircle(screen, x, y, 3, color.RGBA{0xff, 0xc8, 0x00, 0x60}, false)
}

// emptySubImage returns a 1x1 white image for use with DrawTriangles.
func emptySubImage() *ebiten.Image {
	img := ebiten.NewImage(3, 3)
	img.Fill(color.White)
	return img.SubImage(img.Bounds()).(*ebiten.Image)
}

// GetSFOScreenPos returns the screen position of SFO (for label rendering).
func (m *MapRenderer) GetSFOScreenPos() (float32, float32) {
	return m.latLonToScreen(sfoLat, sfoLon)
}

// GetPlaneScreenPos returns the screen position of the plane (for label rendering).
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
		return fmt.Sprintf("%d,000 ft", feet/1000)
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
