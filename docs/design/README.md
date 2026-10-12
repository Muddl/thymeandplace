# Design exploration: three directions for Thyme & Place

Status: Wade picked **C (Blend)** and asked for a warmer, more "modern Farmer's Almanac" take. That is **C2 (Almanac)**, below; C is kept for comparison. Exploration on `cursor/design-explore-6c38`. Not for merge. It exists so Wade can pick a direction.

Brief: take the warmth, colour, character and produce imagery of the late-1800s seed catalogues, but with the cleanliness and order of the modern market stalls (generous white space, a strong grid, crisp signboard-like labels, restraint). It must not look AI-generated. Fonts are open-licence and self-hosted. The CSP is unchanged (`font-src 'self'`, no inline styles or scripts).

## Switching between them

Add `?theme=a`, `?theme=b`, `?theme=c` or `?theme=c2` to any full-page URL. The server remembers the choice in a `tp_theme` cookie (one day) so htmx navigation and reloads keep it; `?theme=off` goes back to the shipped stylesheet. With no theme, nothing changes.

```
/?theme=a
/season?lat=38.7167&lng=-9.1333&place=1&theme=b
/ingredient/tomato?cell=295883&theme=c
```

How it is wired: `internal/web/theme.go` picks the theme; `views.Page` then links `static/design/tokens-X.css`, `base.css` and `variant-X.css` in place of `app.css`. `base.css` holds structure only (a 12-column grid, rhythm, chart mechanics) and reads every font, colour, step and radius from the tokens. Each `variant-X.css` adds that direction's character.

Template changes outside the Page head: a chart key on the ingredient page (hidden by `app.css`, so the shipped look is unchanged) and a one-line plate credit in the footer for A and C. No new copy beyond the key labels and that credit.

## The three directions

### A: Catalogue (leans catalogue)

Rationale: the page reads like a catalogue plate set in white. A fat-face display face (Abril Fatface) is used only for the wordmark, place and ingredient names and group titles; everything else is a plain Franklin-style grotesque. Entries are numbered in vermilion and the page is held by double rules, both borrowed from catalogue price lists. The home page has one chromolithograph tomato resting on the header rule. Vermilion carries peak season and the links; ultramarine carries year-round.

| Token | Value | Role |
|---|---|---|
| `--font-display` | Abril Fatface | Wordmark, place and ingredient names, group titles |
| `--font-text` | Libre Franklin (variable) | All other copy and UI |
| `--c-ground` | `#ffffff` | Page |
| `--c-ink` | `#1a2130` | Text, frames, rules |
| `--c-accent` | `#d3351d` | Vermilion: links, focus, peak season, entry numbers |
| `--c-spot` | `#1e4e9c` | Ultramarine: primary button, year-round |
| `--s-1` to `--s-7` | .25, .5, .875, 1.25, 2, 3.5, 6 rem | Spacing scale (loose) |
| `--radius` | `0` | Printed edges stay square |

### B: Stall (leans market stall)

Rationale: the page is built from the stall's parts. A black fascia with a vermilion edge, a framed white signboard for search, group titles as black sign strips carrying a colour tab, and each group's list as a ruled two-column crate. Labels are condensed capitals (Barlow Condensed), text is Barlow. There is no catalogue art at all, so it is the most restrained of the three and the closest to the stall photographs.

| Token | Value | Role |
|---|---|---|
| `--font-display` | Barlow Condensed Bold, capitals | Signboards: wordmark, headings, month cells, labels |
| `--font-text` | Barlow 400 and 600 | All other copy |
| `--c-ground` | `#ffffff` | Signboard white |
| `--c-ink` | `#121417` | Timber frame black |
| `--c-accent` | `#dc3a1b` | Price-tag vermilion: button, links, peak season |
| `--c-spot` | `#1f7a46` | Crate green: year-round |
| `--s-1` to `--s-7` | .25, .5, .75, 1, 1.5, 2.5, 4 rem | Spacing scale (tight and even) |
| `--radius` | `2px` | Timber corners |

### C: Blend (balanced)

Rationale: one idea from each side, kept small. Slab display type (Bevan) and one tomato plate on a marigold label tile come from the catalogues. Wide, bold capitals for labels (set in Archivo's expanded width), a black top frame and a plain white grid come from the stall. Group titles hang in the left column like signs over a stall bay. It is the quietest way to keep both.

| Token | Value | Role |
|---|---|---|
| `--font-display` | Bevan | Wordmark, place and ingredient names only |
| `--font-text` | Archivo (variable, weight and width) | Body copy; expanded bold capitals for every label |
| `--c-ground` | `#fbfbf9` | Page (neutral white, no warm cast) |
| `--c-ink` | `#16181b` | Text, top frame, rules |
| `--c-accent` | `#d8371c` | Vermilion: links, focus, peak season |
| `--c-spot` | `#eab12c` | Marigold: label tile, category tag, year-round |
| `--s-1` to `--s-7` | .25, .5, .75, 1.25, 2, 3.25, 5.5 rem | Spacing scale (medium) |
| `--radius` | `3px` | One small radius |

### C2: Almanac (C, re-coloured; Wade's pick, iteration 1)

Wade's feedback on C: it read as a black-and-white newspaper. He wanted a modern Farmer's Almanac: more colour and warmth, restrained and clean. C2 keeps C's type (Bevan and Archivo), grid and spacing, and replaces the black with six earthy colours, each with one job.

Rationale: the page is a pale sage-stone, not cream, so colour comes from bands rather than from the ground. Each season state is a tinted band with a solid label tab, and that colour is carried into the left rail, the row rules and the 12-month chart, so a state looks the same everywhere. The header is a pale green band under a field-green frame, and the footer is a slate band. Almanac motifs are used sparingly: a woodcut tomato bush on the home page (printed in field green), ruled almanac-style rows, equinox and solstice ticks over the month chart, and a quiet "≈" marking estimates.

| Token | Value | Role | Contrast on page ground (`#f4f7f1`) |
|---|---|---|---|
| `--font-display` | Bevan | Wordmark, place and ingredient names only | |
| `--font-text` | Archivo (variable, weight and width) | Body copy; expanded bold capitals for every label | |
| `--c-ground` | `#f4f7f1` | Page: a light sage-stone | |
| `--c-ink` | `#1d2a25` | Text, and the single dark mark (the current month). A green-black, not black. | 13.8:1 |
| `--c-field` | `#2d6a42` | Field green: **in season**, wordmark, primary button, top frame | 6.0:1 |
| `--c-marigold` | `#e6a323` | Harvest marigold: **peak season**. Always paired with ink text (6.8:1), never used as text. | n/a |
| `--c-slate` | `#38607f` | Sky slate: **year-round**, footer band | 6.2:1 |
| `--c-brick` | `#9a3b2d` | Beet brick: **from storage**, links, focus | 6.4:1 |
| `--s-1` to `--s-7` | .25, .5, .75, 1.25, 2, 3.25, 5.5 rem | Spacing scale (same as C) | |
| `--radius` | `3px` | One small radius | |

Derived tints (no extra colours): muted text is ink at 78% (6.95:1 on the ground); bands are a state colour at 15% over the ground; the header band is field at 12%; the peak band is marigold at 18% over the header tint, because marigold alone tints to cream and mixed into the sage it reads as straw.

State colours, one place each: peak = marigold, in season = field green, year-round = slate, from storage = brick (an outline cell in the chart, a solid tab on the list). Out of season is a pale slate wash.

Accessibility: an in-browser audit of every text node on the three pages at 1280 and 390 px found none below WCAG AA (lowest ratio 5.05:1, large text held to 3:1). Worst-case pairs, computed from the tokens: muted text on the peak band 5.3:1, brick links on it 4.9:1, field green on it 4.6:1. The chart's peak cell has a dark-gold edge so it does not depend on marigold-against-ground contrast (2.0:1), and every cell carries its month letter, with the full name and state in its `title` and in the key.

Estimate marking: each row's "Estimate" tag is visually hidden (kept in the DOM for screen readers) and replaced by a small "≈" at the row's end. The page says it once, in the line under the place name ("≈ Estimates from local climate: how we estimate"), which ties the symbol to the note. **This is a content and honest-copy decision** (the house rule is that every result is labelled as an estimate), so it needs Wade's sign-off before it goes anywhere near the shipped theme.

Almanac motifs: the woodcut is a cut-out of the "New Dwarf Champion Tomato" engraving, drawn as an alpha mask so it takes the field-green token. The ticks are a small SVG mask (`static/design/art/almanac-ticks.svg`) drawn for this project, with the equinoxes and solstices at fixed calendar positions. They are hemisphere-neutral because they are unlabelled dates, and the key says what they are. I left out moon-phase markers: a phase per month would be wrong without real ephemeris data.

### Shared chart encoding

The 12-month chart uses only the accent and spot colours, so the palette stays at four. Peak is solid accent, in season a lighter tint of it, from storage an accent outline, year-round the spot colour, out of season a grey wash. A black bar under the cell marks the current month. The key beneath the chart spells it out.

## Self-critique against the anti-AI list (A, B and C)

| Risk | A: Catalogue | B: Stall | C: Blend |
|---|---|---|---|
| Cream or paper ground, serif plus terracotta | Closest to this silhouette: serif headings and a warm red. Held off by a pure white ground, a print-red (not terracotta) accent, a blue second colour and a sans body. | No serif at all; ground is white. | Slab serif for names only, on a neutral ground. Accent is the same print red. |
| Purple or blue gradients | None. No gradients anywhere (the double rules are `border-style: double`). | None. | None. |
| Inter or system fonts everywhere | Libre Franklin and Abril Fatface, both self-hosted. System fonts appear only as fallbacks. | Barlow and Barlow Condensed. | Archivo and Bevan. |
| Equal three-card grids | None. Entries are ruled rows in two columns, not cards. | No cards, but each group is the same crate form (a black strip plus a bordered two-column list) repeated. Unequal heights soften it. It is the one to watch. | None. Group labels sit in a left rail and entries are ruled rows. |
| Uniform rounded shadows | No shadows. Radius 0. | No shadows. Radius 2px. | No shadows. Radius 3px. |
| Emoji icons | None. The only symbols are the typographic arrows ← and →. | Same. | Same. |
| Filler copy | Only the existing tagline. New text is the chart key and one credit line. | Same. | Same. |


### C2 against the same list, and its own risks

- **Cream, paper and serif plus terracotta:** the ground is a cool sage-stone, not cream. The one warm band (peak) is built on the sage so it reads as straw, not beige. Brick is used for links and storage only. The slab serif is for names only.
- **Gradients, shadows, emoji:** none. The woodcut and ticks are masks, not gradients.
- **Fonts:** Bevan and Archivo, self-hosted (as C).
- **Equal three-card grids:** none; bands hold ruled rows, and band heights follow their content.
- **Filler copy:** the only new text is the equinox and solstice key note and the woodcut credit.
- **Risks:** it is greener than the brief's "warmth" may suggest, because green is also the brand colour. Marigold only appears as the peak band, a tab and a chart cell, so it is easy to turn up or down. The peak band is the one place that can look drab (khaki) next to the green and slate bands. The quiet "≈" is a content decision, above. The ticks are unlabelled unless the reader finds the key note, so they are decoration to some readers. The woodcut is a single image cut from an 1890 page and would want a proper redraw or a small set before shipping.

Other things a reviewer should know (A, B and C):

- **The "Estimate" badge on every row** is required by the honest-copy rule but is visual noise in all three. It is styled as quietly as the rule allows. A real fix is a content decision (for example one line per group), not a styling one.
- **A** has the most character and the highest risk of reading as "heritage". **B** is the most distinctive and least warm. **C** is the safest and the least memorable; it needs the tomato tile and the slab to avoid looking generic.
- **The plate** is a cut-out of one tomato from the 1897 A. W. Livingston's Sons cover. It is a flat red disc at small sizes, so C's version is deliberately a tile. A shipped version would want a purpose-drawn crop or two or three produce plates, not a single shared one.
- **Dark mode** is not designed; the variants force `color-scheme: light`. The shipped `app.css` has a dark scheme.
- **Fonts are Latin subsets.** Place names outside Latin (for example Polish or Turkish) would fall back to a system face. Latin-extended subsets are needed before shipping.
- **Not reviewed on a real device**; checked in Chrome at 1280 and 390 px with no horizontal overflow and no console or CSP errors.

## Assets and credits

| Asset | Where | Licence and source |
|---|---|---|
| Abril Fatface, Libre Franklin, Barlow, Barlow Condensed, Bevan, Archivo | `static/design/fonts/` | SIL Open Font License 1.1, Latin subsets from [Fontsource](https://fontsource.org). Notices in `static/design/fonts/LICENSES.txt`. |
| Tomato plate | `static/design/art/tomato-1897.webp` | Cut from the front cover of A. W. Livingston's Sons, *Annual of True Blue Seeds* (Columbus, Ohio, 1897). Public domain. USDA National Agricultural Library scan ([Internet Archive `awlivingstonsson1897livi`](https://archive.org/details/awlivingstonsson1897livi)), part of the Biodiversity Heritage Library collection, via [Wikimedia Commons](https://commons.wikimedia.org/wiki/File:A._W._Livingston%27s_Sons_Annual_of_True_Blue_Seeds,_1897,_cover.jpg). Credited in the footer of A and C. |
| Woodcut tomato bush (C2) | `static/design/art/tomato-woodcut-1890.webp`, `almanac-ticks.svg` | The woodcut is cut from page 67 of Johnson & Stokes, *Garden & Farm Manual* (Philadelphia, 1890), Henry G. Gilbert Nursery and Seed Trade Catalog Collection. Public domain. Biodiversity Heritage Library [page 42592296](https://www.biodiversitylibrary.org/pageimage/42592296), via [Wikimedia Commons](https://commons.wikimedia.org/wiki/File:1890_garden_and_farm_manual_(Page_67)_BHL42592296.jpg). Only the bush engraving is used, not the other illustrations on that page. Credited in the footer of C2. The tick marks are original to this project. |
| Plant Seed Co. 1899 cover | `docs/design/moodboard/` | Public domain, as above. Reference only, not served. |

## Reproducing the screenshots

Seed a database with `content/catalog/launch.json` (the sample catalog from `cmd/devseed` has too few items), then run the server with `TP_PUBLIC_TODAY=2026-08-15` (needs `ADMIN_RP_ID=localhost`) so the season is August in every capture. Add `theme=c2` for the Almanac iteration. Pages used: `/` after typing "lisb" in the search box, `/season?lat=38.7167&lng=-9.1333&place=1&date=2026-08-15`, and `/ingredient/tomato?cell=295883&date=2026-08-15`.
