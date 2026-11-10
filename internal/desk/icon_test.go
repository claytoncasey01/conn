package desk_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/claytoncasey01/conn/internal/desk"
)

// appIconFile holds the icon's size and byte limit, which the page's tests read too
// (src/lib/theme/appIcon.test.ts), so the page never draws an icon the app refuses.
const appIconFile = "../../testdata/app-icon.json"

// The limits in the shared file are the app's.
func TestIconLimitsMatchTheSharedFile(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(appIconFile)
	if err != nil {
		t.Fatalf("reading %s: %v", appIconFile, err)
	}

	var shared struct {
		Pixels   int `json:"pixels"`
		MaxBytes int `json:"maxBytes"`
	}

	err = json.Unmarshal(data, &shared)
	if err != nil {
		t.Fatalf("parsing %s: %v", appIconFile, err)
	}

	if shared.Pixels != desk.IconPixels {
		t.Errorf("%s holds %d pixels, the app %d", appIconFile, shared.Pixels, desk.IconPixels)
	}

	if shared.MaxBytes != desk.MaxIconBytes {
		t.Errorf("%s holds %d bytes, the app %d", appIconFile, shared.MaxBytes,
			desk.MaxIconBytes)
	}
}

// pngOf is a PNG of width by height in one color.
func pngOf(t *testing.T, width, height int) []byte {
	t.Helper()

	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.SetNRGBA(x, y, color.NRGBA{R: 0x3b, G: 0xcf, B: 0xcf, A: 0xff})
		}
	}

	var encoded bytes.Buffer

	err := png.Encode(&encoded, picture)
	if err != nil {
		t.Fatalf("encoding a %dx%d PNG: %v", width, height, err)
	}

	return encoded.Bytes()
}

// An icon is used only when it is a PNG of the icon's size within the byte limit.
func TestIconIsAPNGOfTheIconsSize(t *testing.T) {
	t.Parallel()

	square := pngOf(t, desk.IconPixels, desk.IconPixels)

	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{name: "a PNG of the icon's size", data: square, wantErr: nil},
		{name: "empty", data: nil, wantErr: desk.ErrIcon},
		{name: "not a PNG", data: []byte("GIF89a"), wantErr: desk.ErrIcon},
		{name: "too small", data: pngOf(t, 512, 512), wantErr: desk.ErrIcon},
		{name: "not square", data: pngOf(t, desk.IconPixels, 512), wantErr: desk.ErrIcon},
		// A good icon with bytes after it, which are otherwise allowed: only the limit refuses it.
		{
			name: "over the byte limit", data: padded(square, desk.MaxIconBytes+1),
			wantErr: desk.ErrIcon,
		},
		{name: "cut short", data: square[:len(square)/2], wantErr: desk.ErrIcon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := desk.Icon(tt.data)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Icon: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// padded is data with zero bytes after it, up to length bytes in all.
func padded(data []byte, length int) []byte {
	return append(bytes.Clone(data), make([]byte, length-len(data))...)
}

// pageWords is what a page might put in an icon besides its pixels.
const pageWords = "page words"

// withText is a PNG with a tEXt chunk holding pageWords put in after its header chunk.
func withText(data []byte) []byte {
	// The signature is 8 bytes and the header chunk 25 (length, type, 13 bytes, checksum).
	const afterHeader = 8 + 25

	const text = "Comment\x00" + pageWords

	body := append([]byte("tEXt"), text...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(text)))
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(body))

	withChunk := append(bytes.Clone(data[:afterHeader]), chunk...)

	return append(withChunk, data[afterHeader:]...)
}

// What the page put in the file besides the pixels is not written back.
func TestIconKeepsOnlyThePixels(t *testing.T) {
	t.Parallel()

	square := pngOf(t, desk.IconPixels, desk.IconPixels)

	tests := []struct {
		name string
		sent []byte
	}{
		{name: "bytes after the image", sent: append(bytes.Clone(square), pageWords...)},
		{name: "a text chunk", sent: withText(square)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			icon, err := desk.Icon(tt.sent)
			if err != nil {
				t.Fatalf("Icon: %v", err)
			}

			if bytes.Contains(icon, []byte(pageWords)) {
				t.Error("the icon kept what the page put beside the pixels")
			}

			picture, err := png.Decode(bytes.NewReader(icon))
			if err != nil {
				t.Fatalf("decoding the icon: %v", err)
			}

			got := color.NRGBAModel.Convert(picture.At(512, 512))
			if got != (color.NRGBA{R: 0x3b, G: 0xcf, B: 0xcf, A: 0xff}) {
				t.Errorf("the icon's pixel is %v, not the one sent", got)
			}
		})
	}
}
