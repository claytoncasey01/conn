package app

import (
	"context"
	"io"
	"io/fs"
	"log"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/settings"
)

// LoadSettings reads settings.json once, at boot, and says on stderr what could not be read.
func LoadSettings(dataDir string, logger *log.Logger, stderr io.Writer) *settings.Store {
	return loadSettings(dataDir, logger, stderr)
}

// SeedDry seeds the dry settings when a dry switch is on and there is no settings.json, and
// answers the configuration the run goes on with and what removes the seed.
func SeedDry(
	cfg config.Config, store *settings.Store, stderr io.Writer,
) (config.Config, func()) {
	return seedDry(cfg, store, stderr)
}

// NewHandler builds the server and everything it reads, and starts the session list, which runs
// until ctx ends.
func NewHandler(
	ctx context.Context, cfg config.Config, store *settings.Store, logger *log.Logger,
	bundle fs.FS,
) *server.Server {
	return newHandler(ctx, cfg, store, logger, bundle)
}
