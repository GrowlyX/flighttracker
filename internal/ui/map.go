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
	"github.com/hajimehoshi/ebiten/v2/text/v2"
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

	radarZoom = 10.0 // fixed zoom for ~50nm view
)

// TileKey uniquely identifies a map tile.
type TileKey struct {
	Z, X, Y int
}

// FlightRenderData holds what the map needs to draw one flight.
type FlightRenderData struct {
	Lat, Lon   float64
	Heading    *int
	Ident      string
	IsFeatured bool
}

// MapRenderer draws an OpenStreetMap tile-based map with flight positions.
type MapRenderer struct {
	// Screen region for the map
	x, y, w, h float32

	// Fixed map state — SFO-centered, constant zoom
	zoom float64

	// Tile cache
	tileCache sync.Map // map[TileKey]*ebiten.Image
	tileFetch sync.Map // map[TileKey]bool (in-flight fetches)
	fetchSem  chan struct{}

	// Plane icon
	planeImg *ebiten.Image

	// Label font (set externally)
	labelFont *text.GoTextFace
}

// NewMapRenderer creates a map renderer for the given screen region.
func NewMapRenderer(x, y, w, h float32) *MapRenderer {
	return &MapRenderer{
		x:        x,
		y:        y,
		w:        w,
		h:        h,
		zoom:     radarZoom,
		fetchSem: make(chan struct{}, 4), // max 4 concurrent tile fetches
	}
}

// SetLabelFont sets the font used for callsign labels on the map.
func (m *MapRenderer) SetLabelFont(f *text.GoTextFace) {
	m.labelFont = f
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

// latLonToScreen converts geographic coordinates to screen pixel coordinates.
func (m *MapRenderer) latLonToScreen(lat, lon float64) (float32, float32) {
	z := int(math.Round(m.zoom))
	// Center tile coordinates (SFO)
	cx, cy := latLonToTileXY(sfoLat, sfoLon, z)
	// Point tile coordinates
	px, py := latLonToTileXY(lat, lon, z)

	// Pixel offset from center
	dx := (px - cx) * tileSize
	dy := (py - cy) * tileSize

	sx := m.x + m.w/2 + float32(dx)
	sy := m.y + m.h/2 + float32(dy)

	return sx, sy
}

// IsOnScreen returns true if the given screen coordinates are within the visible map area.
func (m *MapRenderer) IsOnScreen(sx, sy float32) bool {
	return sx >= m.x && sx <= m.x+m.w && sy >= m.y && sy <= m.y+m.h
}

// DrawRadar renders the fixed map with all flights.
func (m *MapRenderer) DrawRadar(screen *ebiten.Image, flights []FlightRenderData, featuredTrail [][2]float64) {
	// Black background for the map area
	vector.DrawFilledRect(screen, m.x, m.y, m.w, m.h, color.Black, false)

	z := int(math.Round(m.zoom))

	// Calculate which tiles we need — centered on SFO
	cx, cy := latLonToTileXY(sfoLat, sfoLon, z)

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
	if m.y > 0 {
		vector.DrawFilledRect(screen, m.x, 0, m.w, m.y, color.Black, false)
	}

	// Draw featured trail
	if len(featuredTrail) > 1 {
		m.drawTrail(screen, featuredTrail)
	}

	// Draw SFO marker
	m.drawAirportMarker(screen, sfoLat, sfoLon)

	// Draw all flights
	for _, f := range flights {
		if f.Lat == 0 && f.Lon == 0 {
			continue
		}
		sx, sy := m.latLonToScreen(f.Lat, f.Lon)
		if !m.IsOnScreen(sx, sy) {
			continue
		}
		if f.IsFeatured {
			m.drawPlane(screen, f.Lat, f.Lon, f.Heading, 36.0, 1.0)
		} else {
			m.drawPlane(screen, f.Lat, f.Lon, f.Heading, 24.0, 0.5)
		}
		// Draw callsign label
		if m.labelFont != nil && f.Ident != "" {
			labelClr := color.RGBA{0xcc, 0xcc, 0xcc, 0xaa}
			if f.IsFeatured {
				labelClr = color.RGBA{0x00, 0xdd, 0xff, 0xff}
			}
			drawText(screen, f.Ident, float64(sx)+20, float64(sy)-8, m.labelFont, labelClr)
		}
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

// drawTrail draws the flight path as a polyline with fading tail.
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
// size controls the target pixel size, opacity controls transparency (0..1).
func (m *MapRenderer) drawPlane(screen *ebiten.Image, lat, lon float64, heading *int, size, opacity float64) {
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

	scale := size / math.Max(imgW, imgH)

	op := &ebiten.DrawImageOptions{}

	// Move origin to center of image for rotation
	op.GeoM.Translate(-imgW/2, -imgH/2)
	op.GeoM.Scale(scale, scale)
	op.GeoM.Rotate(angle)
	op.GeoM.Translate(float64(x), float64(y))

	// Apply opacity
	op.ColorScale.Scale(float32(opacity), float32(opacity), float32(opacity), float32(opacity))

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
