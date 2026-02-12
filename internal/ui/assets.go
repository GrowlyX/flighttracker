package ui

import (
	"bytes"

	_ "embed"
)

//go:embed fonts/Inter-Regular.ttf
var interFontData []byte

// defaultFontData returns the embedded font bytes as a reader.
func defaultFontData() *bytes.Reader {
	return bytes.NewReader(interFontData)
}
