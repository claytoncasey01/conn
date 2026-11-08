package app

import (
	"path/filepath"

	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// storeBytesMax bounds the read of a module's store at launch: far above what a reviews.json or
// myprs.json of years of PRs holds.
const storeBytesMax = 16 << 20

// launchStores are the stores of the modules that resume review sessions, as a launch found them
// before the modules ran: what they launched, and on which model.
type launchStores struct {
	reviews *reviews.File
	myPrs   *myprs.File
}

// readLaunchStores reads Reviews' and My PRs' stores in dataDir, read-only. A store that cannot
// be read is an empty one: none of its sessions is resumed.
func readLaunchStores(dataDir string) launchStores {
	stores := launchStores{reviews: reviews.EmptyFile(), myPrs: myprs.EmptyFile()}

	text, read := readStoreText(filepath.Join(dataDir, "reviews.json"), storeBytesMax)
	if read {
		stores.reviews = reviews.LoadText(text)
	}

	text, read = readStoreText(filepath.Join(dataDir, "myprs.json"), storeBytesMax)
	if read {
		stores.myPrs = myprs.LoadText(text)
	}

	return stores
}

// readStoreText is the text of the regular file at path, opened as readCapped opens it, and
// true; or false when there is none, it cannot be read, or it is more than bytesMax bytes.
func readStoreText(path string, bytesMax int64) ([]byte, bool) {
	text, err := readCapped(path, bytesMax+1)
	if err != nil {
		return nil, false
	}

	if int64(len(text)) > bytesMax {
		return nil, false
	}

	return text, true
}
