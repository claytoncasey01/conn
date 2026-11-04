package main

import (
	"log"

	conn "github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/provided"
	"github.com/claytoncasey01/conn/internal/realpath"
)

// newProvided brings the provided agents and skills up to date and returns the API the Settings
// page installs skills with. It links agents at start only for the real data folder: a dev or
// dry server has a scratch folder, and linking Claude Code's folder to that would leave links
// that break. A problem is logged, never fatal: the server runs without the provided files.
func newProvided(cfg config.Config, logger *log.Logger) *provided.API {
	items, err := provided.ReadCatalog(conn.Agents(), conn.Skills())
	if err != nil {
		logger.Printf("provided agents and skills: %v", err)

		return provided.New(provided.Deps{Layout: provided.Layout{}, Items: nil, Writable: false})
	}

	layout := provided.Layout{Data: cfg.DataDir, Claude: cfg.ClaudeDir}

	report, err := provided.Start(layout, items, linksAtStart(cfg))
	if err != nil {
		logger.Printf("provided agents and skills: %v", err)
	}

	for _, name := range report.Linked {
		logger.Printf("linked the agent %s into %s", name, cfg.ClaudeDir)
	}

	for _, name := range report.Conflicts {
		logger.Printf("the agent %s is not linked: something else is at its place in %s",
			name, cfg.ClaudeDir)
	}

	for _, path := range report.Removed {
		logger.Printf("removed %s: Conn no longer provides it", path)
	}

	return provided.New(provided.Deps{Layout: layout, Items: items, Writable: !cfg.Dry.Any()})
}

// linksAtStart is whether this server links agents on start: only for the real data folder, and
// never in a dry run.
func linksAtStart(cfg config.Config) bool {
	if cfg.Dry.Any() {
		return false
	}

	return realpath.Resolve(cfg.DataDir) == realpath.Resolve(cfg.DataDirDefault)
}
