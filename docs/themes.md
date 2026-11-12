# Writing a theme for conn

A theme is a folder. It holds a `theme.json` that sets colors, type and shape, and names any animated effects it wants. It can also hold a `theme.css` (see [Trust levels](#trust-levels)). conn turns the folder into a look, and shows it in the Theme menu beside the built-in ones. You never touch the app's code, and a theme can be a dozen lines long, because anything you leave out comes from Default.

## Start from the template

```sh
# from the repo root
mkdir -p ~/.config/conn/themes
cp -R themes/_template ~/.config/conn/themes/my-theme
conn theme check my-theme
```

Then switch back to the conn tab (the theme list is read again when the tab gets focus) and pick "My theme" in the Theme menu. Edit `theme.json`, run the check, and switch back to the tab to see the change.

`~/.config/conn/themes/` is `$CONN_DATA/themes/` when `CONN_DATA` is set. `make dev` sets `CONN_DATA` to `~/.config/conn-dev` unless you have, so for the dev server the folder is `~/.config/conn-dev/themes/` and the UI is at `http://127.0.0.1:5173`. `conn theme check` reads the same variable, so run it as `CONN_DATA=~/.config/conn-dev conn theme check my-theme` to find a theme there by id, or give it the folder's path. The folder name has to be the theme's `id`, so rename the folder if you change the `id` inside the file. Folders that start with `_` or `.` are skipped, which is why the template (`themes/_template/`) is not listed as a theme in conn and cannot be picked by mistake.

## What goes in the folder

```
my-theme/
  theme.json    required: the manifest and the tokens
  theme.css     optional: your own CSS, in your own folder or a built-in only
```

Only regular files are read. A link in place of the folder or either file is refused, and a theme whose `theme.json` is bigger than 64 KB or whose `theme.css` is bigger than 256 KB is refused. At most 100 themes of your own are listed (the first 100 by folder name; the rest are dropped, and the list says so).

## The manifest

```json
{
  "id": "my-theme",
  "name": "My theme",
  "version": "1.0.0",
  "author": "Your name",
  "license": "MIT",
  "description": "A warm paper look.",
  "modes": ["light", "dark"],
  "accent": "free",
  "fonts": ["IBM Plex Sans", "IBM Plex Mono"],
  "tokens": { "light": {}, "dark": {} },
  "effects": []
}
```

| Field | What it is |
|---|---|
| `id` | 1 to 40 characters of `a-z`, `0-9` and `-`, not starting with `-`. It must equal the folder name. A theme of your own cannot reuse the id of a built-in theme. |
| `name` | What the menu shows. Not empty, up to 80 characters. |
| `version` | Not empty, up to 40 characters. |
| `author`, `license` | Up to 200 and 100 characters. They must be present, but may be empty strings. |
| `description` | Up to 1000 characters. Must be present, but may be an empty string. |
| `modes` | `["light"]`, `["dark"]` or both. A theme with one mode ignores the Light / Dark / System control and always shows that mode. |
| `accent` | `"free"`, `"fixed"` or `{ "variants": [...] }`. See [Modes and accent](#modes-and-accent). |
| `fonts` | A list (it may be empty) of up to 8 names from the [font allow-list](#fonts), the ones the page should load. |
| `tokens` | The values, one object per mode. May be left out. |
| `effects` | Up to 8 effects by name. See [Effects](#effects). May be left out. |

Every field above except `tokens` and `effects` has to be in the file, or the theme is refused.

## Tokens

A token is a named slot. Every slot is set in `tokens.light` or `tokens.dark` (only for a mode your theme lists in `modes`). A slot you leave out takes Default's value for the same mode, so set only what makes your theme yours. A few slots follow your own theme instead of Default when you leave them out: `ac-soft` and `ac-line` follow your `ac`, `edge` follows your `mu`, and `stall` is `warn` and `bad` mixed half and half.

How far a slot reaches differs: most of the interface reads the colors, type and shape slots, while a few slots are only read in the Theme menu so far, and the table says so. A slot no part of the app reads yet still has to be valid, and is there for the parts that will.

The slots from `font-body` down (the backdrop, the header, the stage, the cards, the glyphs and the agent tree's wires) are the look: they change the shape of things, not only their color, so a theme of `theme.json` alone can look nothing like Default. They are keywords and bounded numbers, and conn turns each into its own CSS. A conn from before them refuses a theme that sets one, as an unknown token (and says the theme may need a newer conn); its Themes page leaves such a theme out of the gallery list without a word.

A color is a hex value, `rgb()`, `hsl()` or `oklch()` (with an alpha if you want one), or a color at some percent over transparent, written `color-mix(in oklch, <color> 14%, transparent)`. A length is a number with `px`, `rem` or `em` (a bare `0` is fine). A value may not contain `url(`, `;`, `{`, `}`, `@import` or `expression`, and may not be longer than 300 characters.

| Slot | What it paints | Kind and range |
|---|---|---|
| `bg` | the page background | color |
| `panel` | the surface of cards, panels and lists, across the app | color |
| `raise` | in this build only the Theme menu's open button and selected option, and the agent cards on the Overview page | color |
| `sunk` | inset wells, in a handful of places: thread panels, a few rows and detail panels, the Theme menu | color |
| `hover` | the wash under a hovered agent-switcher tab (and the archive notice's back button); nothing else yet | color |
| `line` | the unlit track of a progress bar, and the Theme menu's dividers | color |
| `line2` | the borders of the Theme menu's controls; nothing else yet | color |
| `tx` | body text | color |
| `bright` | titles and text that should stand out; several status colors are also mixed toward it for readable labels | color |
| `mu` | secondary text and labels | color |
| `onac` | text on a filled block of color: the filled buttons and badges | color |
| `ac` | the accent: focus rings, the border of a hovered row, progress bars, the text cursor, the active navigation tab | color |
| `ac-soft` | the wash under a hovered navigation tab, the ring around a hovered Go button, and a ring in the Theme menu | color, derived from `ac` if left out |
| `ac-line` | the border of the Theme menu's open button; nothing else yet | color, derived from `ac` if left out |
| `ok` | healthy and done states: a finished agent, a PR that is ready, a saved draft | color |
| `warn` | states that need a look: a waiting prompt, options, the alerts switch | color |
| `bad` | failures and errors | color |
| `idle` | an agent waiting for its next message (the IDLE dot) | color |
| `info` | a running agent or run (the RUNNING dot, the research cards) | color |
| `k-bug` | the Bug chip on the Open work page | color |
| `k-epic` | the Epic chip on the Open work page, and a series color on the agent page | color |
| `k-task` | the Task chip on the Open work page | color |
| `k-story` | the Story chip on the Open work page, and a series color on the agent page | color |
| `k-subtask` | the Subtask chip on the Open work page, and the CODE step of a ticket run | color |
| `teammate` | the TEAMMATE tag, and a series color on the agent page | color |
| `role-lead` | the lead agent in the tree; it is also the highlight hue of the navigation and of many page tints | color |
| `role-agent` | an agent in the tree | color |
| `role-advisor` | the advisor in the tree; it is also a tint on several pages (the advisor page, review rows, the archived mark) | color |
| `edge` | the chrome hue: the borders of buttons, panels and rows (mostly at a low alpha) and HUD lines | color, follows `mu` if left out |
| `stall` | an agent that stopped without finishing, and the INTERRUPTED state of a piece of work | color, `warn` and `bad` mixed half and half if left out |
| `focus` | the keyboard focus ring and the text cursor | color, follows `ac` if left out |
| `meter-track` | the empty track of the meters (context, tool mix, progress) | color, follows `line` if left out |
| `meter-lit` | a meter's filled stretch | color, follows `ac` if left out |
| `meter-lead` | the lead's meters | color, follows `role-lead` if left out |
| `font-ui` | the interface font: the page body and the HUD labels | a font stack, see [Fonts](#fonts) |
| `font-mono` | code, paths and log text on the pages and in the footer | a font stack, see [Fonts](#fonts) |
| `case` | the letter case of the labels (they are written in sentence case, "Agents running"), and of navigation, buttons, headings, table headers and the HUD while `chrome` is `styled` (never of content such as a transcript). It is a CSS text-transform, so a screen reader may read a button's name in the theme's case ("MY PRS" under Neon, "my prs" under Matrix): the words are the same, only their case differs | `none`, `lowercase` or `uppercase` |
| `tracking` | the letter spacing of those same elements, and of most small labels | a length in `em`, -0.05 to 0.3 |
| `radius` | the corner rounding of cards, buttons, inputs and chips | a length in `px`, 0 to 24 |
| `led` | the shape of the status dots | `circle`, `diamond` or `square` |
| `border-style` | the line style of the borders of cards, rows and buttons | `solid`, `dashed` or `dotted` |
| `lift` | the shadow under the Theme menu's panel and its selected option, and under the cards, panels, stat tiles and rows when `card-shadow` is `lift` (the default) | `none`, or 1 to 4 shadows, each with one color and 2 to 4 lengths, none beyond 64px |
| `glow` | how strong the glow around status dots and highlighted text is; 0 is none | a number, 0 to 1 |
| `blur` | the frosted-glass blur behind a panel drawn from `panel` (not the cards, whose blur is `card-fill`'s, nor the top bar, whose frost is `header`'s); above 0 it is on | a length in `px`, 0 to 16 |
| `motion` | how arrivals and departures of agents and rows move: `power` is a line, a flash and a flicker, `calm` is a plain fade | `calm` or `power` |
| `font-body` | the font of the page body: `ui` is `font-ui`, `mono` is `font-mono` (the HUD labels are `font-ui` either way) | `ui` or `mono` |
| `chrome` | `styled` gives navigation, buttons, headings, labels and the HUD the `case` and `tracking` tokens; `authored` leaves them as the page writes them, so only the labels written in sentence case take `case` | `styled` or `authored` |
| `text-glow` | a phosphor glow on all text, in `tx` with a bright core; 0 is none | a number, 0 to 1 |
| `cursor` | the text cursor: a thin `bar` or a `block` the height of the line | `bar` or `block` |
| `hover-lift` | whether cards, rows and the start button rise or slide a little under the pointer | `off` or `on` |
| `meter` | the meters: one `smooth` bar, or `segmented` into 5px blocks | `smooth` or `segmented` |
| `backdrop` | the pattern drawn over `bg` behind every page, in the `edge` color: a square grid, horizontal lines, a field of dots, or nothing | `none`, `grid`, `lines` or `dots` |
| `backdrop-size` | the spacing of that grid, those lines or dots | a length in `px`, 12 to 96 |
| `backdrop-strength` | how strong that pattern is; 1 is the most it can be (lines at 9% `edge`, dots at 30%), so text over it stays easy to read | a number, 0 to 1 |
| `orbs` | three large soft glows behind the page, in colors each page chooses from your role colors | `none`, `soft` or `bright` |
| `orbs-tint` | the orbs' colors: `roles` lets each page pick them from your role colors, `accent` and `edge` paint all three in that token | `roles`, `accent` or `edge` |
| `backdrop-scan` | a fine scan-line strip over the whole page, in `bright`; 1 is a 2.2% line every 3px, 0 is none | a number, 0 to 1 |
| `header` | the top bar: `flat`, `frost` (a blur of what is behind it), `rule` (a 2px line in `edge` along its bottom edge) or `glint` (frost, and a lit line there in `edge`, `role-lead` and `role-advisor`) | `flat`, `frost`, `rule` or `glint` |
| `stage` | the Overview agent tree's panel: `plain`, `dots` (a dot grid) or `hud` (the dot grid, glows behind the lead and advisor, ruler ticks on its edges, a perspective floor; the rings round the lead are `tree-orbits`) | `plain`, `dots` or `hud` |
| `stage-backing` | a dense `bg` under the agent tree's stage, so an effect behind the page (the rain) does not print through its wires; 0 is none | a number, 0 to 1 |
| `card-fill` | the fill of cards, panels, stat tiles and rows: `plain` is `panel`; `wash` is a gradient from the card's own state color (at most 16% of it, see-through) into `panel`, so the page shows under its start; the contrast check measures `tx` and `mu` over the strongest one laid on the page (with its pattern and orbs) and on `panel`; `glass` is `panel` at `glass-tint` over a blur of what is behind, boosted by `glass-saturate`, measured over the same page (and the wallpaper, if any). Under the system's Reduce transparency setting a glass card is `panel`, solid, and nothing frosts; Conn.app passes the Mac's setting on itself, since WebKit does not | `plain`, `wash` or `glass` |
| `glass-tint` | how much of `panel` a `glass` card carries; 0 is clear glass. Left out, 0.55 | a number, 0 to 0.6 |
| `glass-blur` | how much a `glass` card blurs what is behind it. Left out, 10px | a length in `px` only, 0 to 40 |
| `glass-saturate` | how much a `glass` card boosts the color behind it; 1 is none. The boost also lifts a light page a little (6% per unit past 1) and deepens a dark one (10%), so text keeps its contrast; the contrast check measures both. A theme that sets any of the three glass slots to other than its default gets its glass on every card, on the agent tree's stage, and once only where a card sits inside another | a number, 1 to 2.4 |
| `glass-edge` | light along every card's edge: a lit line along the top and left, a fainter one along the bottom and right, and a soft glow inside, in white (a dark mode takes under half of it); 0 is none. The contrast check measures `tx` and `mu` over the glow at its strongest | a number, 0 to 1 |
| `card-sheen` | a soft white highlight on every card, at the top left, that follows the pointer while Effects are on and the system allows motion; 0 is none. The contrast check measures `tx` and `mu` under its center, with the edge's glow | a number, 0 to 1 |
| `wallpaper-strength` | a wallpaper behind every page, fixed to the window: up to five soft fields of color, in `wallpaper-1` to `wallpaper-5`, at this strength; 0 is none. The contrast check samples it all over the window, with the glass over it | a number, 0 to 1 |
| `wallpaper-1` | the wallpaper's field at the top left (the fields from `wallpaper-1` to `wallpaper-5` lie in that order, the first on top). Left out, Default's | color |
| `wallpaper-2` | the wallpaper's field at the top right. Left out, Default's | color |
| `wallpaper-3` | the wallpaper's field at the bottom right. Left out, Default's | color |
| `wallpaper-4` | the wallpaper's field at the bottom left. Left out, Default's | color |
| `wallpaper-5` | the wallpaper's field at the middle. Left out, Default's | color |
| `card-corner` | the corners of those cards: `round` at `radius`, `square`, `chamfer` (all four cut on a slant by `card-cut`) or `notch` (the top right and bottom left cut). A cut redraws the border along the cut and moves the keyboard focus ring, a selected card's outline and the agent cards' ports inside the card, where the cut cannot hide them; it also clips the card's outer shadow. Under the system's forced colors (Windows high contrast) there is no cut, so the border stays whole | `round`, `square`, `chamfer` or `notch` |
| `card-cut` | how deep a `chamfer` or `notch` cuts; at most 20px, which every card's padding clears, so a cut never reaches its text. Corner `brackets` sit on the square corners a cut removes: pair a cut with `ticks` or `gutter` | a length in `px` only (a `rem` or `em` would grow with the reader's font past the bound), 4 to 20 |
| `card-frame` | the card's frame: `none`, a `hairline` border, `bold` (a second line in `line2` just inside it), `double` (a second line 3px inside) or `rule` (only a line along the top, doubled in `line2`) | `none`, `hairline`, `bold`, `double` or `rule` |
| `card-marks` | marks on the main panels: corner `brackets`, edge `ticks` at the middle of each side, a `gutter` bar down every card's left edge, or `none`; all in the card's own color | `none`, `brackets`, `ticks` or `gutter` |
| `card-stripe` | a lit stripe along the top edge of cards and stat tiles, in the card's own color | `off` or `on` |
| `card-rim` | how much of the card's state color its border takes; at 0 every border is `line`, at 1 a waiting session's border is all `warn` | a number, 0 to 1 |
| `card-bloom` | a soft glow from inside each card's edges, in `tx`; 0 is none | a number, 0 to 1 |
| `card-glow` | a glow around each card in its own color; 1 is the most. Left out, it follows your `glow` | a number, 0 to 1 |
| `card-shadow` | the shadow under cards: `none`, `lift` (the `lift` token) or `halo` (a glow in the card's own color, at `glow` strength) | `none`, `lift` or `halo` |
| `glyph-arrow` | the arrows on links and buttons that go forward or back (`▸ ◂`, `› ‹`, `→ ←`, or none); a link to another page takes `→ ←` under `triangle`, and the keyword's own arrows otherwise | `triangle`, `chevron`, `arrow` or `none` |
| `glyph-bullet` | the mark before a session's last activity and other status lines (`▸`, `•`, `–`, `❯`, or none) | `triangle`, `dot`, `dash`, `prompt` or `none` |
| `glyph-sep` | the separator in section labels such as SESSIONS // 2 SHOWN (`//`, `·`, `\|`, `—`) | `slash`, `dot`, `bar` or `dash` |
| `glyph-brand` | the mark in the brand tile at the top left. The Conn mark, drawn as `conn` (the app icon's drawing), `conn-tube` (a neon tube), `conn-pixel` (on a pixel grid) or `conn-phosphor` (glowing phosphor with scanlines). `conn-tube` and `conn-phosphor` light the tile: it turns to the accent's ink with the mark glowing in the accent, which reads best in a dark theme. Or a glyph instead of the mark: `▟█▙`, `❯_`, `◆`, or an empty tile. Left out, the tile shows `conn` | `conn`, `conn-tube`, `conn-pixel`, `conn-phosphor`, `blocks`, `prompt`, `diamond` or `none` |
| `wire-style` | the wires of the Overview agent tree: a `line`, a `cable` (a dim sheath with the line as its core and a sheen along it), a `rail` (two thin rails, a sheath cut down its middle) or a `dash` | `line`, `cable`, `rail` or `dash` |
| `wire-width` | how thick those wires are (the thinner links to the advisor are three quarters of it; a cable or rail is wider than its line) | a length in `px` only, 1 to 6 |
| `wire-head` | how a wire ends where it meets a card: an `arrow`, a `plug` (a connector block), a `dot`, a `bar` across it, or `none` | `arrow`, `plug`, `dot`, `bar` or `none` |
| `wire-crawl` | dashes crawling down the trunk to the agents, and back along the return while an agent comes home | `off` or `on` |
| `wire-flow` | what travels a wire while an agent works or returns, and between the lead and the advisor right after a consult: `packets` (a dot and its trail), `blocks` (square data bits and their trail), `pulse` (a lit segment sweeping the wire), `glyphs` (a short stream of characters, `wire-glyphs`) or `none` | `packets`, `blocks`, `pulse`, `glyphs` or `none` |
| `wire-glyphs` | the characters a `glyphs` flow carries, from a fixed set: `binary` (1 0), `hex`, `kana` (half-width katakana) or `ascii` (# % @ &); never text of the theme's own | `binary`, `hex`, `kana` or `ascii` |
| `wire-signal` | the color of what travels the wires: `roles` (each in its wire's role color; what returns to the lead is `tx`), or one color for all: `lead` (the lead's role color, so nothing turns near-black on a light page), `bright` or `tx` (so a theme can keep the signal off a color its effects use, such as Matrix's rain) | `roles`, `lead`, `bright` or `tx` |
| `wire-speed` | how fast the crawl, the flow, the orbits and the node beat run: 2 is twice as fast | a number, 0.25 to 4 |
| `tree-orbits` | the rings turning round the lead, and the comet on the inner one | `off` or `on` |
| `node-beat` | the junction nodes of the tree swell and fade | `off` or `on` |

A name that is not in the table is refused (`tokens.light: unknown token "colour"`), which catches typos. A value out of range is refused too, so a theme cannot hide a control: `radius` of `40px` fails with `tokens.light.radius is out of range (0 to 24px)`.

## Modes and accent

`modes` says which of light and dark the theme has. Each mode has its own `tokens`; the tokens for one mode are never used for the other.

`accent` says who chooses the accent color:

- `"free"`: the person using the theme picks one of the accent swatches in the menu, and conn fills `ac`, `onac`, `ac-soft` and `ac-line` from the pick. It chooses `onac` itself so the text on the accent is readable at every swatch. Your `tokens` still apply to everything else. Use `free` when your look works with any accent (Default does).
- `"fixed"`: the accent is whatever your `tokens` say (or Default's, if you set none). No swatches are shown.
- `{ "variants": [...] }`: your theme ships 1 to 8 named looks, such as CRT's Green, Amber and White. The menu shows a Variant picker instead of swatches. A variant's `tokens` are laid over the mode's `tokens`, so a variant only lists what differs. If the choice is unset or names a variant that no longer exists, the first variant is used.

```json
"accent": {
  "variants": [
    { "name": "Green", "tokens": { "ac": "#8bffad", "tx": "#8bffad" } },
    { "name": "Amber", "tokens": { "ac": "#ffbc00", "tx": "#ffbc00" } }
  ]
}
```

A variant name is up to 40 characters and cannot repeat. Every variant is held to the contrast rules, on its own.

## Fonts

A font slot is a stack of family names. Only these families may appear, along with a few generic ones:

- `Chakra Petch`
- `JetBrains Mono`
- `IBM Plex Sans`
- `IBM Plex Mono`
- `Share Tech Mono`

`font-ui` may also use `system-ui`, `ui-sans-serif` and `sans-serif`, and must end in `system-ui`. `font-mono` may also use `ui-monospace` and `monospace`, and must end in `ui-monospace`. The stack ends in a system font so that a font that fails to load never leaves the page without text.

```json
"font-ui": "'IBM Plex Sans', ui-sans-serif, system-ui",
"font-mono": "'JetBrains Mono', ui-monospace"
```

A font only reaches the page if it is also listed in the manifest's `fonts`, since that list is what conn asks for (from Google Fonts) while your theme is in use. Unlike a token, `fonts` is not inherited from Default: a theme that leaves a font slot to Default (as the template does) lists Default's fonts, `IBM Plex Sans` and `IBM Plex Mono`, itself, or the page falls back to the system font.

```json
"fonts": ["IBM Plex Sans", "JetBrains Mono"]
```

A name outside the allow-list is refused in `fonts` and in a font slot. To add a family, open a pull request that adds its name to `ALLOWED_FONTS` in `src/lib/theme/tokens.ts` and its weights to `WEIGHTS` in `src/lib/theme/fonts.ts`. It has to be a family Google Fonts serves.

## Effects

An effect is animation or an overlay that conn ships and your theme turns on by name. A theme never brings code: it gives a name and options, and a name conn does not know is refused. The library is closed, and a new effect arrives by pull request: a file in `src/effects/`, and its name added to `KNOWN_EFFECTS` in `src/lib/theme/tokens.ts`, because a theme that names an effect that is not on that list is refused.

```json
"effects": [
  { "name": "scanlines", "options": { "opacity": 0.14 } },
  { "name": "vignette" }
]
```

| Effect | What it does | Options |
|---|---|---|
| `rain` | falling glyph columns on a canvas, in two depths | `density` 0 to 1 (default 0.6), `speed` 0.1 to 5 (default 1), `color` the name of the token the trails use (default `ac`; a name with no value in the theme makes `rain` fail to start) |
| `scanlines` | thin dark lines over the page, still or crawling slowly down it, over an optional aperture grille | `opacity` 0 to 0.2 (default 0.14; it caps the lines and the grille together), `pitch` 2 to 12 pixels between lines (default 3), `crawl` 0 for still lines (the default) or 1 to 20 seconds for the lines to drift eight pitches, `grille` `true` or `false` (default `false`): a fine vertical aperture in the theme's `ac` |
| `vignette` | the page fades toward `bg` at the corners | `strength` 0 to 0.6 (default 0.55), `glass` `true` or `false` (default `false`): the tube's face, a rounded edge with dark corners and a soft highlight at the top left in `bright` |
| `roll` | the tube's refresh band: a soft lighter stripe drifting down the screen and round again, in `tx` and `bright` | `strength` 0 to 0.06 (default 0.04), how light its brightest line is; `period` 3 to 30 seconds a pass (default 8) |
| `grain` | the phosphor's fine noise over the page: a tile of specks in `tx` and `bg`, made in code, shifted a few times a second | `strength` 0 to 0.12 (default 0.06), `hz` 1 to 8 steps a second (default 6) |
| `flicker` | a faint dark veil that wobbles a few times a second | `amount` 0 to 0.06 (default 0.03), `hz` 1 to 8 steps a second (default 4) |
| `glow` | reserved: the name is accepted, but no effect of that name exists yet, so it draws nothing. For glowing text, set the `glow` token instead. | none |

A number outside its range is clamped to it, so a theme cannot ask for more than the cap. An option of the wrong type (a word where a number goes) makes that one effect fail to start and leaves the others and the page alone. An option an effect does not know is ignored. `conn theme check` does not check the type of an effect's options (any number, boolean or short plain string passes), so a wrong-type option shows up only in the app, as an effect that does not start. Each of `scanlines`, `vignette`, `flicker`, `roll` and `grain` draws once even if you list it twice. None of them flashes: every one stays within a few percent of the page's brightness, `flicker` and `grain` step at most 8 times a second, and a `roll` pass takes at least three seconds.

### Layers

An effect draws into one of two layers, which its own file picks with its `layer` export (`export const layer = 'above'`); an effect that does not say is `behind`.

- `behind` sits underneath the page (`rain`). The page's panels are drawn over it, so a glass panel (a `blur` above 0, a translucent `panel`) lets it show through.
- `above` sits over the page (`scanlines`, `vignette`, `flicker`, `roll`, `grain`) and takes no clicks or keys: every control under it works as usual.

You do not choose the layer in `theme.json`; it follows the effect.

### Effects and reduced motion

The Theme menu has an Effects switch. It starts on, unless the system asks for less motion, until the person turns it on or off. Under reduced motion with Effects on, `rain` draws one still frame, `flicker` and `roll` draw nothing, `grain` is one still tile, and `scanlines` (still, without its crawl) and `vignette` still draw. A hidden tab pauses the moving ones.

The switch reaches every theme's own motion too, not only the effects: conn marks the page `data-fx="on"` or `"off"` (at first paint as well, from the saved choice), and every keyframed animation, the look's and a `theme.css`'s alike, runs only on `data-fx="on"` and only while the system does not ask for less motion. So with Effects off Neon holds still (its ring, beam, glint, wire dashes, orbits and node beat), meters and cursors stop, and what travels the agent tree's wires is hidden rather than frozen mid-wire. A `theme.css` keyframe has to sit behind the same gate, `@media (prefers-reduced-motion: no-preference){ :where([data-theme="<id>"][data-fx="on"]) .x{animation:…} }`, with its still frame outside it. conn's tests hold the built-in themes to that; a `theme.css` of your own is not checked for it, so one that skips the gate keeps moving with Effects off.

## Contrast rules

A theme that is hard to read is refused, not warned about. Every theme is checked in each of its modes (and for each accent variant), with your tokens laid over Default's:

| Color | On | Needs at least |
|---|---|---|
| `tx` | `bg`, `panel` and `raise` | 4.5 to 1 |
| `onac` | `ac` | 4.5 to 1 |
| `line2` | `panel` | 1.5 to 1 |
| `mu` | `bg` and `panel` | 3 to 1 |

There is no waiver: when a palette fails, change the palette. A color with some transparency is measured on what sits under it: `panel` over `bg`, `raise` over `panel`, `ac` over `bg`, and `bg` over black (dark) or white (light). The colors in this table have to be plain colors (hex, `rgb()`, `hsl()` or `oklch()`); a `color-mix(...)` there cannot be measured and is reported as such.

An example failure, a light mode whose body text is too pale:

```json
"light": { "bg": "#f6f3ee", "panel": "#fffdf9", "raise": "#ffffff", "tx": "#9a8f80" }
```

```
my-theme: light: tx on bg is 2.87:1, needs at least 4.5:1
my-theme: light: tx on panel is 3.12:1, needs at least 4.5:1
my-theme: light: tx on raise is 3.17:1, needs at least 4.5:1
```

The fix is a darker `tx`. `#4a4238` passes all three:

```json
"light": { "bg": "#f6f3ee", "panel": "#fffdf9", "raise": "#ffffff", "tx": "#4a4238" }
```

## Trust levels

Where a theme comes from sets what it may carry.

| Level | Carries |
|---|---|
| Gallery install | `theme.json` only: tokens and named effects. A gallery folder (one with a `.source.json`) never has its `theme.css` served, and `conn theme check` fails a gallery folder that has one. |
| Your own folder | `theme.json`, and optionally a `theme.css` in `~/.config/conn/themes/<id>/`. A theme with a `theme.css` is marked "custom CSS" in the menu and asks "Use anyway" before it is applied. It asks again whenever the file changes. |
| Built in | `theme.json` and `theme.css`, in `themes/<id>/` in this repo, by pull request. A built-in's `theme.css` never asks. |

A `theme.css` is the one place you can write raw CSS, and it is global once it is applied: it can change how any part of conn looks, including what a button says. Scope every rule to your theme so it does nothing for the others:

```css
[data-theme="my-theme"] .card { border-width: 2px; }
```

The scoping is enforced by a test for built-in themes; in your own folder it is on you. Only use CSS you wrote or read.

Reach for the look tokens before CSS: the backdrop, the top bar, the stage, the cards' fill, corners, frame, marks, stripe and shadow, and the glyphs are all tokens, and a theme of tokens alone can share them. A `theme.css` is for what tokens cannot say, such as motion. These are the classes the pages give it:

- `.page`, the wrapper of every page. The body paints `bg` under it; anything drawn here must stay see-through, because the effects layer sits between the body and the page.
- `.surf`, every card, panel, stat tile and row, with its state as data: `--rim` is its border color (a session waiting for input has a `warn` one), `--wash` and `--halo` its tinted fill and glow, `--c` its own color. Draw a frame with `outline` or an inset `box-shadow`, not a wider border, so the card keeps its size.
- `.brk`, the main panels that `card-marks` marks; `.stripe`, the line along the top edge of a card or tile.
- `.page-scan`, `.page-head`, `.glint`, `.stage`, `.stage-ticks`, `.stage-floor`, `.stage-glow`, `.orbit` and `.orbit2`: the pieces `backdrop-scan`, `header` and `stage` draw.
- `.g-fwd`, `.g-back`, `.g-bullet`, `.g-sep`, `.g-brand` and `.g-brand-mark` (the Conn mark's SVG, shown by the `conn` values of `glyph-brand`, with one group per drawing: `.bm-solid`, `.bm-tube` and `.bm-pixel`): the glyphs.

Neon's `theme.css` is the example: it holds only Neon's motion and a few colors no token has.

## Previewing

Add the theme and the mode to the address to see a look without changing what is saved in your browser:

```
http://127.0.0.1:7777/?theme=<id>&mode=<mode>
```

`mode` is `light`, `dark` or `system`; either part may be left out. The look holds for that load only, and the stored choice is not touched. An id that is not in the list shows Default. Touching any control in the Theme menu drops the pin and saves that choice as usual. A pinned theme of yours with a `theme.css` is never asked about, so the preview applies the CSS only if you already accepted that exact file in this browser; otherwise it shows the tokens alone.

## Checking a theme

```sh
# from the repo root
conn theme check my-theme        # a theme of yours, found by id
conn theme check ./path/to/dir   # or a folder
conn theme check                 # every built-in theme
conn theme check --gallery ./path/to/dir   # a folder as the gallery would take it (see below)
```

Inside a conn checkout it judges the built-in themes from that checkout's `themes/` folder, so an edit to a built-in is checked as it is now, and a folder given by its path there is judged as a built-in; anywhere else it uses the built-in themes conn was built with.

It runs the same judgment conn does when it lists themes, and prints `my-theme: ok` or one line per problem, then exits 0 when everything passed, 1 when something did not, and 2 for bad usage (an unknown option, or `--gallery` with nothing to check). For a folder with a `theme.css` it also prints a note that conn asks before using it. Run it before you share a theme.

`--gallery` is the same check under the rules of the `conn-themes` repo, which are stricter than for a theme of yours: the folder must hold `theme.json` alone (a `theme.css`, a `.source.json` or any other file fails it, each on its own line), the file must be UTF-8 without a byte order mark and within 64 KB, the folder name must be an id of 1 to 40 characters (a-z, 0-9 and `-`, not starting with `-`) and equal to the manifest's `id`, and the `id` must not be a built-in's. It calls the same function `conn theme index` does, so a folder that passes `--gallery` is one the index builder accepts. It needs a folder or an id to check, and a theme found by id is looked up in `themes/` first, then in `~/.config/conn/themes/`.

## The built-in themes

| Theme | Modes | Accent | Effects |
|---|---|---|---|
| Default | light and dark | free | none; its agent tree is thin lines with a slow pulse in the lead's color (every theme of tokens alone inherits this unless it sets its own wires) |
| Neon | dark | fixed | none; its ring, live pulse, beam and glint are in its `theme.css`; its tree's dashes, comets, orbits and node beat are wire tokens |
| Matrix | dark | fixed | `rain`; its tree streams hex characters in `bright` along thin wires, over a dense backing so the rain does not print through |
| CRT | dark | variants: Green, Amber, White | the tube: `scanlines` (crawling, over a grille), `vignette` (glass), `flicker`, `roll` and `grain`; cabled wires with plug ends and blocks of data in the agent tree; its blinking block cursor is in its `theme.css` |

Read their `theme.json` files in `themes/` for working examples. Default sets every slot, which is why a theme can leave any of them out.

## The gallery

The Themes page (`#/themes`, the Theme menu's `Browse themes` link) lists the community themes in a public GitHub repo, `claytoncasey01/conn-themes`, next to the ones you have. Each card previews the theme and offers what fits its state: `Install` for one you do not have, `Update` when the index lists a newer version of one you installed from there, `Use` to switch to it, and `Uninstall` for a theme that came from the gallery. An install is the theme's `theme.json` and a `.source.json` in `~/.config/conn/themes/<id>/`, so it is listed in the Theme menu like a theme of your own. The page only ever reads `raw.githubusercontent.com` (the README says exactly when and what is checked), and `CONN_THEME_INDEX` changes where or whether:

| `CONN_THEME_INDEX` | What the gallery does |
|---|---|
| not set | reads `claytoncasey01/conn-themes` |
| `owner/repo` | reads that repo instead (a fork, or your own, built the way below) |
| anything else, or blank | reads the default repo, as if it were not set |
| `off`, `false`, `0`, `none`, `no` or `disabled` (any case) | reads nothing and offers no install: the page says the gallery is off, and themes you installed stay |

### Adding a theme to conn-themes

A theme is added by a pull request to the `conn-themes` repo that adds one folder:

```
my-theme/
  theme.json
```

1. Write the theme as for your own folder (see [Start from the template](#start-from-the-template)), and keep its `id` equal to the folder name.
2. Run `conn theme check --gallery ./my-theme` from a conn checkout until it prints `my-theme: ok`. It applies the gallery's rules (the index builder's, and the install's judgment of the file, contrast included, with no waiver); plain `conn theme check ./my-theme` is looser, and passes a `theme.css` the gallery would refuse.
3. Put nothing else in the folder. A gallery theme is tokens and named effects only: a `theme.css` is never installed, and the index builder refuses a folder that has one (or any file besides `theme.json`). The file has to be UTF-8 without a byte order mark and no larger than 64 KB, and its `id` must not be a built-in's.
4. Open the pull request. The `author`, `version` and `description` in `theme.json` are what the card shows, so raise `version` for a change that should reach people who installed it as an update.

### How the index is built

Nobody edits `index.json` by hand. It lists each theme with the commit it is pinned at and the sha256 of its `theme.json`, and the app installs only a file that hashes to that value. It is made by a script in this repo, run from a conn checkout against a checkout of `conn-themes` at the commit to pin:

```sh
conn theme index --src ../conn-themes --out ../conn-themes-out --commit <40 hex> --repo claytoncasey01/conn-themes
```

It judges every `<id>/theme.json` as the app does, refuses the whole run (writing nothing) if any folder fails, refuses a commit that is not 40 lowercase hex characters, and needs `--out` to be a new or empty folder. It copies each `theme.json` into `<out>/<id>/` (byte for byte as authored) and writes `<out>/index.json` as compact JSON, listing every theme at the commit given. The app refuses an index over 512 KB and ignores entries beyond the first 500, and every entry carries its theme's whole manifest, so the size cap is what binds first (roughly 250 typical themes, fewer if they set every token); the builder refuses a run that would go over either limit. Every top-level folder of `--src` must be a theme folder (a stray folder is an error, not skipped), except dot folders (`.git`, `.github`); loose files at the root (the README, a license, `.gitattributes`) are not themes and are ignored. A source with no theme folder at all is refused, so a mistyped `--src` cannot produce an empty index. `gallery-seed/` in this repo holds the first three themes, `ember`, `paper` and `blueprint` (a look made of tokens alone: a drafting grid, chamfered cards with ruler ticks, a rule under the top bar, arrows and its own separators, and dimension-line wires whose dashes crawl under a slow pulse), and the repo's README; it is the starting contents of `conn-themes`.

The maintainer, `claytoncasey01`, publishes `conn-themes`: merges the pull requests, builds the index at the merged commit and commits `index.json` to `main`, which is where the app reads it. Use the real commit, from a checkout whose files are exactly what was committed (no line-ending conversion): an entry whose file does not hash to its `sha256` is installed by no one. `gallery-seed/.gitattributes` (`*.json -text`) keeps git from converting line endings in the repo for that reason; an update that takes a theme's folder aside during an install is described in the README.
