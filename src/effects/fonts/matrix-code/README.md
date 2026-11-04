# Conn Matrix Code

The glyph font the Matrix theme's `rain` effect draws with: 66 glyphs plus .notdef in 4,068 bytes (`ConnMatrixCode.woff2`, family `Conn Matrix Code`).

## What is in it

- 56 half-width katakana, U+FF66 to U+FF9D. Each is the full-width kana outline from the source font, mirrored and squeezed horizontally to 0.75 of its width, then mapped to the half-width code point.
- The ten digits 0-9, upright.

The shapes are NOT the film's font. The real Matrix Code font is not openly licensed, so it is unfit for this repo and this font is made from an open one instead.

## Provenance

- Source: `MPLUS1Code[wght].ttf` from `github.com/google/fonts` (path `ofl/mplus1code`), M+ 1 Code, by the M+ FONTS Project.
- Upstream sha256: `ff68678c5bd7e9d9d6ab6d57e4355aabe30f6b8f8bff9bd59baf6a7807dcfd36`.
- Instanced at `wght 300`, then subset to the glyphs above and saved as woff2. `build.py` is the whole recipe.

## License

SIL Open Font License 1.1, in `OFL.txt` beside the font. The name table kept the copyright (name id 0) and license (name ids 13, 14) entries, and the family is renamed `Conn Matrix Code`, so the OFL reserved-name rule is not engaged.

## Rebuilding

fonttools is a manual tool and not a dependency of this repo. Nothing in `package.json` or CI runs `build.py`; the built woff2 is committed. To rebuild, with the upstream `MPLUS1Code[wght].ttf` in the current directory:

```
python3 -m venv v && v/bin/pip install fonttools brotli && v/bin/python build.py "MPLUS1Code[wght].ttf" ConnMatrixCode.woff2
```
