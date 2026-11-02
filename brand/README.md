# VCRX Matrix Green Brand Kit | Revision 3

Primary accent: **Matrix green #008F11**. Background: **black #000000**.

## What changed

The standalone **V** now uses the same two-part angular construction and diagonal negative-space cut as the V in the approved VCRX wordmark. This version replaces the previously solid, unbroken V in all V-only logo and app-icon variants.

All other established artwork (the full VCRX wordmark, wordmark app icon, Apple TV-style rectangle, marketing lockup, and overall brand styling) remains unchanged in geometry and layout, with only the accent green recolored from #00FF41. The original board is retained for comparison; an updated board is also included.

## Folders

- `logos/`: split-V vector master (SVG), green PNG sizes, white alternate; original full wordmark PNG.
- `icons/`: black/green square icons in SVG and 512/1024 PNG, standalone transparent V and small 32px favicon; unchanged original wordmark app icon.
- `tv/`: original 16:9 Apple TV-style black tile, preserved without alteration.
- `reference/`: updated green visual reference board and original reference board with dark-green color applied.

The split-V SVG geometry was manually reconstructed from the shape in the approved raster wordmark; it is **not** the original editable vector source. For pixel-identical production masters, recreate all VCRX lettering in a vector editor using a single verified master.

Suggested fonts: Rajdhani Bold/SemiBold (headlines), Inter Regular/Medium (UI). No font files included.

Tagline: **Your media. Your hardware. Your rules.**

## Color choice and use
This revision uses **#008F11** for the primary VCRX accent, replacing the brighter #00FF41 throughout the graphics. This is a dark CRT/digital-rain inspired green, not an official licensed Matrix color. The approved split-V geometry, wordmark silhouette, original icon layouts, and Apple TV rectangle composition are unchanged. The original imagery had imperfect pixel artifacts; this is a color edit, not a vector recreation.

## In VCRX

This kit replaced the orange one (VCRX Orange #FF5F00) on 2026-11-02. `colors.json` is the palette the apps follow.

`tools/make-assets.py` cleans the split V (`logos/VCRX-standalone-V-split-1160.png`) and the wordmark (`logos/VCRX-primary-wordmark-original.png`), sets every inked pixel to exactly #008F11, and writes:

- `android/res/`: launcher icons (adaptive, monochrome, legacy), TV banners, the in-app wordmark and mark, `values/brand_colors.xml`. Copy them into `tv/app/src/main/res/`.
- `../server/web/static/img/`: the web app's 192/512 and maskable icons, Apple touch icon, favicon (the split V) and wordmark.
- `generated/`: the wordmark on black for the README.

On screens, text on the green is black: #008F11 gives black text 4.9:1 (WCAG AA) and white only 4.3:1.
