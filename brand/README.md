# gflow-cli Brand Kit

This directory contains the approved, dependency-free brand assets for
gflow-cli. Open `preview.html` in a browser to inspect the complete kit.

## Contents

| Path | Purpose |
| --- | --- |
| `BRAND.md` | Naming, construction, color, spacing, accessibility, and copy rules |
| `LICENSES.md` | Licensing and third-party attribution notes |
| `provenance.json` | Machine-readable identity source and asset inventory |
| `tokens.json` | Portable visual tokens and complete fixed mark geometry |
| `tokens.css` | CSS custom-property projection of visual styling tokens; complete geometry remains in JSON |
| `logos/` | Light, inverse, lockup, wordmark, and monochrome SVGs |
| `icons/` | Tiled favicon and app-icon SVGs |
| `og/og-default.svg` | Canonical editable 1200 by 630 social image source |
| `og/og-default.png` | Canonical 1200 by 630 raster social image |
| `preview.html` | Local visual inventory; no build step or network access required |

## Choosing An Asset

- Use `logos/lockup.svg` on white and light backgrounds.
- Use `logos/lockup-inverse.svg` on Cockpit Obsidian and dark backgrounds.
- Use a standalone mark only when the product name appears next to it or an
  accessible name is supplied.
- Use the files in `icons/` without adding another background tile.
- Use monochrome artwork only when the output process requires one color.
- Use `og/og-default.png` for social metadata; regenerate it from the SVG source.

All SVGs are standalone and have no linked images or embedded credentials.
The kit introduces no runtime or package dependency.

## Documentation Projections

The GitHub Pages site consumes deployment copies so `docs/` remains a complete
publishable directory:

| Canonical file | Documentation projection |
| --- | --- |
| `icons/favicon.svg` | `../docs/favicon.svg` |
| `logos/mark.svg` | `../docs/logo-mark.svg` |
| `og/og-default.svg` | `../docs/og-default.svg` |
| `og/og-default.png` | `../docs/og-default.png` |

Keep each pair identical whenever the canonical source changes.

## Regenerating The Social PNG

From the repository root, use the pinned renderer and then copy the canonical
PNG to its documentation projection:

```powershell
npx --yes sharp-cli@5.2.0 -i brand/og/og-default.svg -o brand/og/og-default.png -f png -c 9
Copy-Item -LiteralPath brand/og/og-default.png -Destination docs/og-default.png
```

The output must remain exactly `1200 x 630`. After changing the source or
renderer version, render a fresh PNG and run the brand checks to verify its
geometry and source-to-projection parity.
