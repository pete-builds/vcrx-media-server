#!/usr/bin/env python3
"""Derive the app assets from the VCRX brand kit PNGs in brand/.

The kit ships raster artwork, not vector masters, and some files carry faint
stray marks around the art. This script cleans the two source marks (the
split standalone V and the primary wordmark), sets every inked pixel to
exactly VCRX Green #008F11 (brand/colors.json), and renders:

  brand/android/res/   launcher icons (adaptive, monochrome, legacy), TV banners,
                       the in-app wordmark and mark, and the brand colours
                       (copy them to tv/app/src/main/res/)
  server/web/static/img/  the web app's icons, favicon and wordmark
  brand/generated/     a wordmark on a black field for the README

Run from the repo root after changing the kit:
  nix-shell --run "python3 brand/tools/make-assets.py"
Output is deterministic, so a rerun with an unchanged kit changes nothing.
"""
import os
import sys
from collections import deque

from PIL import Image, ImageDraw

GREEN = (0x00, 0x8F, 0x11)
BLACK = (0, 0, 0)
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
DENSITIES = {"mdpi": 1, "hdpi": 1.5, "xhdpi": 2, "xxhdpi": 3, "xxxhdpi": 4}


def clean(path, color=GREEN, min_alpha=40, min_area_ratio=0.002):
    """Load a single-colour mark, drop faint and tiny stray marks, recolour."""
    im = Image.open(path).convert("RGBA")
    w, h = im.size
    alpha = im.getchannel("A")
    a = alpha.load()
    mask = [[a[x, y] >= min_alpha for x in range(w)] for y in range(h)]
    seen = [[False] * w for _ in range(h)]
    keep = [[False] * w for _ in range(h)]
    comps = []
    for y in range(h):
        for x in range(w):
            if mask[y][x] and not seen[y][x]:
                q = deque([(x, y)])
                seen[y][x] = True
                pts = []
                while q:
                    cx, cy = q.popleft()
                    pts.append((cx, cy))
                    for nx, ny in ((cx + 1, cy), (cx - 1, cy), (cx, cy + 1), (cx, cy - 1)):
                        if 0 <= nx < w and 0 <= ny < h and mask[ny][nx] and not seen[ny][nx]:
                            seen[ny][nx] = True
                            q.append((nx, ny))
                comps.append(pts)
    total = sum(len(c) for c in comps)
    for c in comps:
        if len(c) >= total * min_area_ratio:
            for x, y in c:
                keep[y][x] = True
    out = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    o = out.load()
    for y in range(h):
        for x in range(w):
            if keep[y][x]:
                o[x, y] = color + (a[x, y],)
    return out.crop(out.getbbox())


def fit(mark, box_w, box_h):
    """Scale mark to fit inside box_w x box_h, keeping its aspect."""
    s = min(box_w / mark.width, box_h / mark.height)
    size = (max(1, round(mark.width * s)), max(1, round(mark.height * s)))
    return mark.resize(size, Image.LANCZOS)


def centered(canvas, mark):
    canvas.alpha_composite(mark, ((canvas.width - mark.width) // 2, (canvas.height - mark.height) // 2))
    return canvas


def save(img, *parts):
    path = os.path.join(ROOT, *parts)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    img.save(path, optimize=True)
    return path


def write(text, *parts):
    path = os.path.join(ROOT, *parts)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(text)


def main():
    v = clean(os.path.join(ROOT, "logos", "VCRX-standalone-V-split-1160.png"))
    word = clean(os.path.join(ROOT, "logos", "VCRX-primary-wordmark-original.png"))
    res = ("android", "res")

    for name, s in DENSITIES.items():
        # Adaptive icon: a 108dp canvas whose inner 66dp circle is always visible.
        canvas = round(108 * s)
        fg = centered(Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0)), fit(v, 50 * s, 50 * s))
        save(fg, *res, f"mipmap-{name}", "ic_launcher_foreground.png")
        white = Image.new("L", fg.size, 255)
        mono = Image.merge("RGBA", (white, white, white, fg.getchannel("A")))
        save(mono, *res, f"mipmap-{name}", "ic_launcher_monochrome.png")
        # Legacy icons for launchers without adaptive support.
        px = round(48 * s)
        sq = Image.new("RGBA", (px, px), (0, 0, 0, 0))
        ImageDraw.Draw(sq).rounded_rectangle((0, 0, px - 1, px - 1), radius=round(px * 0.18), fill=BLACK + (255,))
        save(centered(sq, fit(v, px * 0.66, px * 0.66)), *res, f"mipmap-{name}", "ic_launcher.png")
        rd = Image.new("RGBA", (px, px), (0, 0, 0, 0))
        ImageDraw.Draw(rd).ellipse((0, 0, px - 1, px - 1), fill=BLACK + (255,))
        save(centered(rd, fit(v, px * 0.58, px * 0.58)), *res, f"mipmap-{name}", "ic_launcher_round.png")

    # Android TV launcher banner, 320 x 180 dp.
    for name, s in (("xhdpi", 1), ("xxxhdpi", 2)):
        bw, bh = 320 * s, 180 * s
        banner = Image.new("RGBA", (bw, bh), BLACK + (255,))
        save(centered(banner, fit(word, bw * 0.72, bh * 0.5)).convert("RGB"), *res, f"drawable-{name}", "banner.png")

    save(fit(word, 1200, 1200), *res, "drawable-nodpi", "brand_wordmark.png")
    save(fit(v, 512, 512), *res, "drawable-nodpi", "brand_mark.png")

    adaptive = """<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background"/>
    <foreground android:drawable="@mipmap/ic_launcher_foreground"/>
    <monochrome android:drawable="@mipmap/ic_launcher_monochrome"/>
</adaptive-icon>
"""
    write(adaptive, *res, "mipmap-anydpi-v26", "ic_launcher.xml")
    write(adaptive, *res, "mipmap-anydpi-v26", "ic_launcher_round.xml")
    write("""<?xml version="1.0" encoding="utf-8"?>
<!-- Generated by brand/tools/make-assets.py from the VCRX brand kit. -->
<resources>
    <color name="ic_launcher_background">#000000</color>
    <color name="brand_accent">#008F11</color>
    <color name="brand_black">#000000</color>
</resources>
""", *res, "values", "brand_colors.xml")

    # Web app icons (server/web/static/img), named by the manifest.
    # "any": the wordmark in a green-edged rounded black tile on clear, the
    # kit's square app icon. Maskable and Apple touch: edge to edge black,
    # since Android and iOS cut their own shape; the wordmark stays inside the
    # centre circle (radius 40%) that every mask keeps. The favicon is the
    # split V in the same tile: four letters do not read at 16px.
    web = ("..", "server", "web", "static", "img")

    def tile(px, mark, w, h):
        t = Image.new("RGBA", (px, px), (0, 0, 0, 0))
        edge = max(2, round(px * 0.03))
        ImageDraw.Draw(t).rounded_rectangle((0, 0, px - 1, px - 1), radius=round(px * 0.2), fill=GREEN + (255,))
        ImageDraw.Draw(t).rounded_rectangle((edge, edge, px - 1 - edge, px - 1 - edge), radius=round(px * 0.2) - edge, fill=BLACK + (255,))
        return centered(t, fit(mark, px * w, px * h))

    for px in (192, 512):
        save(tile(px, word, 0.74, 0.5), *web, f"icon-{px}.png")
    save(tile(64, v, 0.62, 0.62), *web, "icon-64.png")
    for px, name in ((512, "icon-maskable-512.png"), (192, "icon-maskable-192.png"), (180, "apple-touch-icon.png")):
        full = Image.new("RGBA", (px, px), BLACK + (255,))
        save(centered(full, fit(word, px * 0.66, px * 0.5)).convert("RGB"), *web, name)
    # The wordmark in the top bar, sign-in and picker (shown up to 220px wide),
    # at three times that for sharp phone screens.
    save(fit(word, 660, 660), *web, "wordmark.png")

    readme = Image.new("RGBA", (1400, 400), BLACK + (255,))
    save(centered(readme, fit(word, 1100, 240)).convert("RGB"), "generated", "vcrx-wordmark-on-black.png")
    print("ok")


if __name__ == "__main__":
    sys.exit(main())
