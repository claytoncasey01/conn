package hosted_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
)

// A record's name and folder are bounded, and its folder is written plainly.
func TestCheckRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		change  func(record *hosted.Record)
		wantErr error
	}{
		{"a record Conn writes", func(*hosted.Record) {}, nil},
		{"a name of the most bytes", func(record *hosted.Record) {
			record.Name = strings.Repeat("n", hosted.NameBytesMax)
		}, nil},
		{"a name of a byte more", func(record *hosted.Record) {
			record.Name = strings.Repeat("n", hosted.NameBytesMax+1)
		}, hosted.ErrBadRecord},
		{"a folder of the most bytes", func(record *hosted.Record) {
			record.Dir = "/" + strings.Repeat("d", hosted.DirBytesMax-1)
		}, nil},
		{"a folder of a byte more", func(record *hosted.Record) {
			record.Dir = "/" + strings.Repeat("d", hosted.DirBytesMax)
		}, hosted.ErrBadRecord},
		{"a folder with a slash after it", func(record *hosted.Record) {
			record.Dir += "/"
		}, hosted.ErrBadRecord},
		{"a folder that climbs", func(record *hosted.Record) {
			record.Dir += "/../KEY-2"
		}, hosted.ErrBadRecord},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			changed := record(0)
			tt.change(&changed)

			err := hosted.CheckRecord(changed)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err %v, want %v", err, tt.wantErr)
			}
		})
	}
}
