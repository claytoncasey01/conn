# conn-themes

Community themes for [conn](https://github.com/claytoncasey01/conn), the dashboard for Claude Code sessions. conn's Themes page reads this repo's `index.json` and installs a theme from the commit the index pins it at.

## What is here

```
index.json          the list conn reads: made by a script, never edited by hand
<id>/theme.json     one folder per theme; the folder name is the theme's id
.gitattributes      `*.json -text`: see below
README.md
```

A theme is `theme.json` alone: colors, type, shape and named effects. No CSS, no images, no fonts (it can name the fonts conn already bundles). conn installs the file only if it hashes to the value in `index.json` and passes the same checks as a theme in `~/.conn/themes/`, contrast included.

## Add a theme

1. Fork this repo and add `<id>/theme.json`. Start from `themes/_template/` in the conn repo; [docs/themes.md](https://github.com/claytoncasey01/conn/blob/main/docs/themes.md) lists every token.
2. From a conn checkout run `conn theme check --gallery ./path/to/<id>` until it prints `<id>: ok`.
3. Open a pull request with the one new folder. Do not touch `index.json`; the maintainer rebuilds it.

Rules the build enforces, so a pull request that breaks one will not be listed:

- the folder name is the `id` in the file (`a-z`, `0-9` and `-`, up to 40 characters), and it is not the id of a conn built-in (today `default`, `neon`, `matrix` and `crt`)
- `theme.json` is the only file in the folder: no `theme.css`, no dotfiles
- every folder at the root is a theme folder (only dot folders such as `.github` are skipped); loose files like this README are not themes
- UTF-8 without a byte order mark, at most 64 KB
- it passes `conn theme check --gallery`, which applies all of the above and every contrast pair, no waiver
- `version` goes up when a change should reach people who installed it as an update

## For the maintainer

`.gitattributes` marks `*.json` as `-text` on purpose: the index pins the sha256 of each file's exact bytes, so git must never convert its line endings (autocrlf on a contributor's machine would otherwise make an installed file hash differently from the index). Keep it.

After merging, build the index at the merged commit from a conn checkout, with this repo checked out at that commit and its files exactly as committed (no line-ending conversion):

```sh
conn theme index --src ../conn-themes --out ../conn-themes-out --commit "$(git -C ../conn-themes rev-parse HEAD)" --repo claytoncasey01/conn-themes
```

Copy `../conn-themes-out/index.json` over this repo's `index.json` and commit it to `main`. conn reads `https://raw.githubusercontent.com/claytoncasey01/conn-themes/main/index.json` and, for an install, `.../<commit>/<id>/theme.json`.
