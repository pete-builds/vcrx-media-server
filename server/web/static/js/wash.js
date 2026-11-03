// The details page's colour wash: three colours taken from the backdrop, the
// way the Google TV and Plex apps tint the whole page from the picture. The TV
// app does the same sums in WashColors.kt; keep the two in step.
//
// washColors reads an RGBA pixel array (an ImageData's data) and returns
// { base, glow, deep } as [r, g, b]: base from the left half, under the text,
// kept dark so white text reads on it; glow from the top right, the brightest;
// deep from the bottom half, between the two. Vivid pixels count for more than
// grey ones, so a red coat on a grey street gives red rather than brown.
export function washColors(data, width, height) {
  const regions = {
    base: (x, y) => x < width / 2,
    glow: (x, y) => x >= width / 2 && y < height / 2,
    deep: (x, y) => y >= height / 2,
  };
  const out = {};
  for (const [name, inside] of Object.entries(regions)) {
    let r = 0, g = 0, b = 0, weight = 0;
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        if (!inside(x, y)) continue;
        const i = (y * width + x) * 4;
        const [pr, pg, pb] = [data[i], data[i + 1], data[i + 2]];
        const w = 0.15 + saturation(pr, pg, pb);
        r += pr * w; g += pg * w; b += pb * w; weight += w;
      }
    }
    const avg = weight ? [r / weight, g / weight, b / weight] : [0, 0, 0];
    out[name] = tone(avg, LIGHTNESS[name]);
  }
  return out;
}

// How light each colour may be: [min, max] HSL lightness.
const LIGHTNESS = { base: [0.08, 0.2], glow: [0.24, 0.42], deep: [0.12, 0.26] };

function saturation(r, g, b) {
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  return max === 0 ? 0 : (max - min) / max;
}

// tone lifts the colour's saturation and holds its lightness inside [lo, hi].
function tone([r, g, b], [lo, hi]) {
  let [hue, s, l] = rgbToHsl(r / 255, g / 255, b / 255);
  s = Math.min(1, s * 1.3 + 0.08);
  l = Math.min(hi, Math.max(lo, l));
  return hslToRgb(hue, s, l).map((v) => Math.round(v * 255));
}

function rgbToHsl(r, g, b) {
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  const l = (max + min) / 2;
  if (max === min) return [0, 0, l];
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h;
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
  else if (max === g) h = (b - r) / d + 2;
  else h = (r - g) / d + 4;
  return [h / 6, s, l];
}

function hslToRgb(h, s, l) {
  if (s === 0) return [l, l, l];
  const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
  const p = 2 * l - q;
  const f = (t) => {
    if (t < 0) t += 1;
    if (t > 1) t -= 1;
    if (t < 1 / 6) return p + (q - p) * 6 * t;
    if (t < 1 / 2) return q;
    if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6;
    return p;
  };
  return [f(h + 1 / 3), f(h), f(h - 1 / 3)];
}

// applyWash sets --wash-base, --wash-glow and --wash-deep on el from a loaded
// <img>. A picture the canvas cannot read (another origin) leaves the default.
export function applyWash(el, img) {
  try {
    const w = 32, h = 18;
    const canvas = document.createElement("canvas");
    canvas.width = w;
    canvas.height = h;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });
    ctx.drawImage(img, 0, 0, w, h);
    const c = washColors(ctx.getImageData(0, 0, w, h).data, w, h);
    for (const [name, rgb] of Object.entries(c)) el.style.setProperty(`--wash-${name}`, `rgb(${rgb.join(" ")})`);
    el.classList.add("washed");
  } catch {
    // Keep the default wash.
  }
}
