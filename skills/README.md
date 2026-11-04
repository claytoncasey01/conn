# Skills Conn provides

Each folder here with a `SKILL.md` in it is a skill Conn provides. Conn carries them in its
binary, copies them into `~/.config/conn/skills/` and, when you press INSTALL on the Settings page,
links `~/.claude/skills/<name>` to the copy. Nothing is installed until you do.

A skill's folder name is its name: lowercase letters, digits and hyphens. A script in a skill keeps
its executable bit only if it starts with `#!`.
