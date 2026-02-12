package ui

import (
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
	centerLat, centerLon float64
	zoom                 float64
	targetZoom           float64

	// Tile cache
	tileCache sync.Map // map[TileKey]*ebiten.Image
	tileFetch sync.Map // map[TileKey]bool (in-flight fetches)
	fetchSem  chan struct{}
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
		m.centerLat = sfoLat
		m.centerLon = sfoLon
		m.targetZoom = 12
	} else {
		// Center between SFO and plane
		m.centerLat = (sfoLat + planeLat) / 2
		m.centerLon = (sfoLon + planeLon) / 2

		// Calculate zoom to fit both points with padding
		latDiff := math.Abs(sfoLat - planeLat)
		lonDiff := math.Abs(sfoLon - planeLon)
		maxDiff := math.Max(latDiff, lonDiff)

		// More aggressive zoom: tighter framing of the area
		if maxDiff < 0.02 {
			m.targetZoom = 15 // Very close, like just departed
		} else if maxDiff < 0.1 {
			m.targetZoom = 13
		} else if maxDiff < 0.5 {
			m.targetZoom = 11
		} else if maxDiff < 2 {
			m.targetZoom = 9
		} else if maxDiff < 5 {
			m.targetZoom = 7
		} else if maxDiff < 15 {
			m.targetZoom = 5
		} else if maxDiff < 40 {
			m.targetZoom = 4
		} else {
			m.targetZoom = 3
		}
	}

	// Smooth zoom transition
	m.zoom += (m.targetZoom - m.zoom) * 0.1
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
func (m *MapRenderer) Draw(screen *ebiten.Image, planeLat, planeLon float64, heading *int) {
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

	// Draw route line from SFO to plane
	if planeLat != 0 || planeLon != 0 {
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

// drawRouteLine draws a line from SFO to the plane.
func (m *MapRenderer) drawRouteLine(screen *ebiten.Image, lat, lon float64) {
	sfoX, sfoY := m.latLonToScreen(sfoLat, sfoLon)
	planeX, planeY := m.latLonToScreen(lat, lon)

	routeColor := color.RGBA{0x00, 0xbb, 0xff, 0x90}
	vector.StrokeLine(screen, sfoX, sfoY, planeX, planeY, 2.5, routeColor, true)
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

// drawPlane draws the aircraft icon at the given position.
func (m *MapRenderer) drawPlane(screen *ebiten.Image, lat, lon float64, heading *int) {
	x, y := m.latLonToScreen(lat, lon)

	angle := 0.0
	if heading != nil {
		angle = float64(*heading) * math.Pi / 180
	}

	size := float32(14)

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

	planeColor := color.RGBA{0xff, 0xcc, 0x00, 0xff}

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

	// Glow
	vector.DrawFilledCircle(screen, x, y, 4, color.RGBA{0xff, 0xcc, 0x00, 0x50}, true)
}

// emptySubImage returns a tiny white image for DrawTriangles.
func emptySubImage() *ebiten.Image {
	img := ebiten.NewImage(3, 3)
	img.Fill(color.White)
	return img.SubImage(img.Bounds()).(*ebiten.Image)
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
