package conn

import (
	"embed"
	"io/fs"
)

// The agents and skills Conn provides. They are copied into the data folder and linked from
// Claude Code's folder; see internal/provided. An agent is one .md file; a skill is a folder with
// a SKILL.md. skills/README.md keeps the folder, and so this embed, valid while no skill is in it.
//
//go:embed agents/*.md
var providedAgents embed.FS

//go:embed skills
var providedSkills embed.FS

// Agents is the provided agent files, rooted at agents/.
func Agents() fs.FS {
	sub, err := fs.Sub(providedAgents, "agents")
	if err != nil {
		panic(err) // a constant, valid path: cannot fail
	}

	return sub
}

// Skills is the provided skill folders, rooted at skills/.
func Skills() fs.FS {
	sub, err := fs.Sub(providedSkills, "skills")
	if err != nil {
		panic(err) // a constant, valid path: cannot fail
	}

	return sub
}
