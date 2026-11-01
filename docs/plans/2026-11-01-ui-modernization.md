# UI modernization: less chrome, more cinema

Pete's brief (2026-11-01): make the web app and the Google TV app feel like a
premium streaming product, without a rewrite. Smaller, more elegant controls,
consistent corners, artwork first, quieter navigation, restrained orange.

## Audit: the ten biggest problems (main at 4099459)

1. **TV rail.** A 248dp column of full-width uppercase rows; focus floods the
   whole row orange. The heaviest chrome on the TV.
2. **TV has one button.** `BrandButton` is a 6dp outlined rectangle in caps for
   every action; focus fills it orange, adds a white edge and grows 6%. Play
   and Mark watched weigh the same.
3. **TV Details has no backdrop.** A black page, a poster, an orange caps title.
4. **TV seasons, episodes and cast are boxes.** Seasons are a row of full
   buttons, each episode a framed full-width card, the cast graphite tiles.
5. **All caps everywhere.** TV `Label` uppercases buttons, row titles and the
   meta line; web nav, eyebrows and season tabs shout too.
6. **No corner system.** Web: 16 different radii. TV: 9 shapes, buttons 6dp
   against cards 8dp.
7. **Web Play is the same rectangle as everything else.** Frosted secondaries;
   on a phone the actions stretch into full-width slabs.
8. **Orange overload on the web.** Every card hover gets an orange ring.
9. **Box on box on the web.** Cast and extras as tiles, Activity figures in
   panels, square bordered icon buttons, admin tools above the cast.
10. **No shared tokens** for spacing, type sizes or motion (timings from .15s
    to .35s with mixed easing).

## Design system

One vocabulary, two platforms. Web tokens live in `:root` of
`server/web/static/css/app.css`; TV tokens in `tv/.../ui/theme/Tokens.kt`.

| Token | Web | TV | Use |
|---|---|---|---|
| chip | 4px | 4dp | chips, small badges |
| card | 8px | 8dp | artwork |
| control | 10px | 10dp | secondary buttons, inputs, selects |
| panel | 14px | 14dp | dialogs, panels |
| pill | 9999px | 50% | the main action, search, filter chips |
| circle | 50% | circle | icon-only buttons |

- **Buttons:** primary is an orange pill; secondary a muted soft-rounded
  surface; an icon alone sits in a circle with its name for screen readers.
- **Type:** Rajdhani for display titles only (page and hero titles, big
  figures); Inter for everything read. Sentence case; caps only for the small
  orange eyebrow.
- **Colour:** black ground, neutral surfaces stepping up by lightness, orange
  for the main action, keyboard and remote focus, progress and the current-page
  mark. Hover is a quiet white edge, never orange.
- **Motion:** one easing (`cubic-bezier(.2,.7,.2,1)`), three speeds (120, 200,
  320ms). Cards scale 1.04 on hover and focus. Reduced motion turns it all off.
- **TV focus:** stays unmistakable from the couch: focus fills the primary
  pill white-on-black edge, secondary turns white, cards gain an orange ring
  and scale. TV keeps its own sizes (overscan-safe 48dp margins, 10-foot type);
  only the vocabulary is shared.

## Build order

1. **Web** (one PR): tokens; buttons; nav in sentence case with a short orange
   mark; cards without orange hover; slim progress track; season and range
   pickers as pills; cast and extras de-boxed; Activity figures unboxed;
   item page puts episodes, extras, cast and facts before admin tools (Fix
   match stays on top when a link opened it).
2. **TV components**: `Tokens.kt`; `BrandButton` variants (primary pill,
   secondary quiet, icon circle); slim rail; sentence-case labels.
3. **TV Details**: backdrop with gradients; seasons as pills; lighter episode
   rows; de-boxed cast.

## Not changed

Routes, APIs, playback, D-pad focus logic (restorers, requesters, Back
handling), feature set. Class names the web smoke test relies on stay.
