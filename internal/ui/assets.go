package ui

import (
	"bytes"

	_ "embed"
)

//go:embed fonts/SF-Pro-Display-Regular.otf
var sfProRegularData []byte

//go:embed fonts/SF-Pro-Display-Medium.otf
var sfProMediumData []byte

//go:embed fonts/SF-Pro-Display-Bold.otf
var sfProBoldData []byte

//go:embed fonts/SF-Pro-Display-Semibold.otf
var sfProSemiboldData []byte

// regularFontData returns the SF Pro regular font bytes.
func regularFontData() *bytes.Reader {
	return bytes.NewReader(sfProRegularData)
}

// mediumFontData returns the SF Pro medium font bytes.
func mediumFontData() *bytes.Reader {
	return bytes.NewReader(sfProMediumData)
}

// boldFontData returns the SF Pro bold font bytes.
func boldFontData() *bytes.Reader {
	return bytes.NewReader(sfProBoldData)
}

// semiboldFontData returns the SF Pro semibold font bytes.
func semiboldFontData() *bytes.Reader {
	return bytes.NewReader(sfProSemiboldData)
}
