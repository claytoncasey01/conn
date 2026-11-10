package desk

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
)

// The Dock icon the page draws for the app (src/lib/theme/appIcon.ts) and sends as a PNG. Its
// limits are in testdata/app-icon.json too, which the page's tests and these read, so the two
// sides cannot drift apart.
const (
	// IconPixels is the icon's width and height: the largest a macOS icon is drawn at.
	IconPixels = 1024
	// MaxIconBytes bounds the PNG the page sends. A drawn icon is well under a megabyte; this
	// leaves room for any theme's colors while keeping a bad page from handing the app much.
	MaxIconBytes = 4 << 20
)

// ErrIcon is an icon the app will not use; the wrapping error says why.
var ErrIcon = errors.New("the icon cannot be used")

// Icon is the PNG data re-encoded, when data is a PNG of IconPixels square and at most
// MaxIconBytes, and an error wrapping ErrIcon otherwise. Only the pixels Go decoded are
// written back, so nothing else the page put in the file (text chunks, trailing bytes) reaches
// the file the Dock reads. The size is read from the header before the pixels are decoded, so a
// file claiming to be larger is refused without decoding it.
func Icon(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: it is empty", ErrIcon)
	}

	if len(data) > MaxIconBytes {
		return nil, fmt.Errorf("%w: it has %d bytes, over the limit of %d", ErrIcon, len(data),
			MaxIconBytes)
	}

	header, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: it is not a PNG: %w", ErrIcon, err)
	}

	if header.Width != IconPixels || header.Height != IconPixels {
		return nil, fmt.Errorf("%w: it is %dx%d, not %dx%d", ErrIcon, header.Width, header.Height,
			IconPixels, IconPixels)
	}

	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: its pixels cannot be read: %w", ErrIcon, err)
	}

	var encoded bytes.Buffer

	err = png.Encode(&encoded, picture)
	if err != nil {
		return nil, fmt.Errorf("%w: it cannot be written again: %w", ErrIcon, err)
	}

	return encoded.Bytes(), nil
}
