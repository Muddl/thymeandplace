# Design exploration: three directions for Thyme & Place

Status: exploration on `cursor/design-explore-6c38`. Not for merge. It exists so Wade can pick a direction.

Brief: take the warmth, colour, character and produce imagery of the late-1800s seed catalogues, but with the cleanliness and order of the modern market stalls (generous white space, a strong grid, crisp signboard-like labels, restraint). It must not look AI-generated. Fonts are open-licence and self-hosted. The CSP is unchanged (`font-src 'self'`, no inline styles or scripts).

## Switching between them

Add `?theme=a`, `?theme=b` or `?theme=c` to any full-page URL. The server remembers the choice in a `tp_theme` cookie (one day) so htmx navigation and reloads keep it; `?theme=off` goes back to the shipped stylesheet. With no theme, nothing changes.

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

### Shared chart encoding

The 12-month chart uses only the accent and spot colours, so the palette stays at four. Peak is solid accent, in season a lighter tint of it, from storage an accent outline, year-round the spot colour, out of season a grey wash. A black bar under the cell marks the current month. The key beneath the chart spells it out.

## Self-critique against the anti-AI list

| Risk | A: Catalogue | B: Stall | C: Blend |
|---|---|---|---|
| Cream or paper ground, serif plus terracotta | Closest to this silhouette: serif headings and a warm red. Held off by a pure white ground, a print-red (not terracotta) accent, a blue second colour and a sans body. | No serif at all; ground is white. | Slab serif for names only, on a neutral ground. Accent is the same print red. |
| Purple or blue gradients | None. No gradients anywhere (the double rules are `border-style: double`). | None. | None. |
| Inter or system fonts everywhere | Libre Franklin and Abril Fatface, both self-hosted. System fonts appear only as fallbacks. | Barlow and Barlow Condensed. | Archivo and Bevan. |
| Equal three-card grids | None. Entries are ruled rows in two columns, not cards. | No cards, but each group is the same crate form (a black strip plus a bordered two-column list) repeated. Unequal heights soften it. It is the one to watch. | None. Group labels sit in a left rail and entries are ruled rows. |
| Uniform rounded shadows | No shadows. Radius 0. | No shadows. Radius 2px. | No shadows. Radius 3px. |
| Emoji icons | None. The only symbols are the typographic arrows ← and →. | Same. | Same. |
| Filler copy | Only the existing tagline. New text is the chart key and one credit line. | Same. | Same. |

Other things a reviewer should know:

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
| Plant Seed Co. 1899 cover | `docs/design/moodboard/` | Public domain, as above. Reference only, not served. |

## Reproducing the screenshots

Seed a database with `content/catalog/launch.json` (the sample catalog from `cmd/devseed` has too few items), then run the server with `TP_PUBLIC_TODAY=2026-08-15` (needs `ADMIN_RP_ID=localhost`) so the season is August in every capture. Pages used: `/` after typing "lisb" in the search box, `/season?lat=38.7167&lng=-9.1333&place=1&date=2026-08-15`, and `/ingredient/tomato?cell=295883&date=2026-08-15`.
