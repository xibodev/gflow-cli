# gflow-cli Brand

Status: approved

This document defines the public identity for the gflow-cli project. The files
in this directory are the canonical source assets.

## Identity

- Formal product name: **gflow-cli**
- CLI command: **`gflow`**
- Mark name: **Supersonic Stream Ribbons**
- Primary tile: **Cockpit Obsidian**

Use `gflow-cli` when naming the project, repository, package, or brand. Use
`gflow` only for the executable and commands such as `gflow image`.

## Mark Construction

The mark uses a `0 0 100 100` view box and these fixed elements:

- Ring: circle at `50,38`, radius `16`, no fill, 7-unit stroke.
- Stream: `M66 38 L66 64 C66 76 54 82 40 82 C28 82 22 74 22 66`,
  no fill, 7-unit stroke, round caps and joins.
- Velocity accent: `M50 22 C58 22 66 30 66 38`, no fill, 3-unit stroke,
  round caps.
- Endpoint: circle at `66,38`, radius `4.5`.

Do not rotate, skew, crop, rearrange, or redraw these elements. Keep the ring
and stream as one color and retain the contrast between the main ribbon and
the velocity accent.

## Color

| Token | Value | Use |
| --- | --- | --- |
| Supersonic Cyan | `#00E5FF` | Primary ribbon on Cockpit Obsidian or another verified dark surface |
| Accessible Cyan Dark | `#007C91` | Primary ribbon on white and light surfaces |
| Cockpit Obsidian | `#080B10` | Primary tile and dark field |
| Signal White | `#FFFFFF` | Inverse wordmark, velocity accent, and endpoint |

Supersonic Cyan is not approved for text or fine detail on white. Use
Accessible Cyan Dark for the light-surface variant. On light surfaces, the
velocity accent and endpoint use Cockpit Obsidian.

## Variants

- `logos/mark.svg`, `logos/lockup.svg`, and `logos/wordmark.svg` are for light
  surfaces.
- `logos/mark-inverse.svg`, `logos/lockup-inverse.svg`, and
  `logos/wordmark-inverse.svg` are for Cockpit Obsidian or similarly dark
  surfaces.
- `logos/mono-black.svg` and `logos/mono-white.svg` are one-color fallbacks for
  processes that cannot reproduce the two-color mark.
- Files under `icons/` place the inverse mark on the approved Cockpit Obsidian
  tile.

The wordmark is always lowercase and hyphenated: `gflow-cli`. It uses the
existing Public Sans display family at weight 800, with Arial and a generic
sans-serif as fallbacks.

## Spacing And Size

Keep clear space equal to at least 8% of the mark width on every side. Do not
place other type, rules, or graphics inside that area.

- Minimum standalone mark size: 24 CSS pixels.
- Minimum lockup width: 120 CSS pixels.
- Below 120 CSS pixels, use the standalone mark and an accessible text label
  instead of compressing the lockup.
- Favicons are exempt from the standalone minimum and use the supplied tiled
  artwork.

## Accessibility

- Choose the light or inverse asset for the surface; do not recolor it ad hoc.
- When the logo is the only content of a link, provide the accessible name
  `gflow-cli` or `gflow-cli home`.
- When visible text already identifies the product, treat the adjacent mark as
  decorative with an empty alternative.
- Do not put essential information only in the mark or its color.

## Product Copy

Keep public copy tied to implemented behavior and the current README. The
current provider names are Gemini, MiniMax, and Google Flow. Command examples
must use implemented `gflow` subcommands. Do not imply that gflow-cli is an
official product of, endorsed by, or affiliated with any provider.

## Canonical Assets And Projections

`brand/` is canonical. The copies in `docs/favicon.svg`, `docs/logo-mark.svg`,
`docs/og-default.svg`, and `docs/og-default.png` are deployment projections for
GitHub Pages. Update a canonical asset and its projection together. The PNG is
the social-metadata asset and is generated from `brand/og/og-default.svg` using
the pinned command documented in `README.md`.
