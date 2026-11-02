#!/usr/bin/env bash
# Fails when a tracked file names a real home network or a home directory.
# This repo is public: examples use 192.168.1.x and the few documentation
# addresses allowed below, and the real deployment's addresses stay in the
# private workspace. Run from anywhere in the repo.
#
#   scripts/check-private.sh            check the tracked files
#   scripts/check-private.sh FILE...    check these files (the control uses this)
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
if [ "$#" -gt 0 ]; then
  files=("$@")
else
  files=()
  while IFS= read -r -d '' f; do files+=("$f"); done < <(git ls-files -z -- ':!scripts/check-private.sh')
fi
fail=0
# 192.168.0 and .1 are the documentation examples; .16 and .224 name Docker's
# default address pools. Any other 192.168 subnet is someone's real LAN.
pattern='192\.168\.([2-9]|1[0-57-9]|[2-9][0-9]|1[0-9]{2}|2[013-9][0-9]|22[0-35-9])\.|/(home|Users)/[a-z]'
if hits=$(grep -nIE "$pattern" -- "${files[@]}"); then
  echo "check-private: real network or home path in a public file:" >&2
  echo "$hits" >&2
  fail=1
fi
# Other private ranges (10/8, 172.16/12) and Tailscale's 100.64/10: only these
# example values and whole-range names may appear. The first and last address of
# each range are allowed too: the TV's private-address test checks both edges.
allowed='^(10\.0\.0\.0/8|10\.0\.0\.5|10\.0\.2\.2|10\.9\.9\.9|10\.255\.255\.255|172\.16\.0\.0/12|172\.16\.0\.1|172\.31\.255\.255|100\.64\.0\.0/10|100\.64\.0\.1|100\.101\.102\.103|100\.127\.255\.255)$'
other='(^|[^0-9.])(10\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7]))\.[0-9]{1,3}\.[0-9]{1,3}(/[0-9]{1,2})?'
if hits=$(grep -noIE "$other" -- "${files[@]}" | sed -E 's/:[^0-9]?([0-9.\/]+)$/:\1/' | awk -F: -v ok="$allowed" '{ a = $NF; if (a !~ ok) print }') && [ -n "$hits" ]; then
  echo "check-private: private or Tailscale address that is not a documentation example:" >&2
  echo "$hits" >&2
  fail=1
fi
# Screenshots are binary, so the greps above skip them. A script can check the
# text an image carries: metadata (EXIF, XMP, PNG text chunks) names hosts,
# paths and devices, so none may be present, and no address may appear in its
# bytes. What the picture shows is the manual check in docs/screenshots/README.md.
shots=()
for f in "${files[@]}"; do
  case "$f" in docs/screenshots/*.webp | docs/screenshots/*.png | docs/screenshots/*.jpg | docs/screenshots/*.jpeg) shots+=("$f") ;; esac
done
if [ "${#shots[@]}" -gt 0 ]; then
  if hits=$(LC_ALL=C grep -laE "$pattern|$other" -- "${shots[@]}"); then
    echo "check-private: an address or home path inside a screenshot's bytes:" >&2
    echo "$hits" >&2
    fail=1
  fi
  if ! python3 - "${shots[@]}" << 'PY'; then
import struct, sys
bad = 0
for path in sys.argv[1:]:
    b = open(path, "rb").read()
    found = []
    if b[:4] == b"RIFF" and b[8:12] == b"WEBP":
        i = 12
        while i + 8 <= len(b):
            tag, n = b[i:i + 4], struct.unpack("<I", b[i + 4:i + 8])[0]
            if tag in (b"EXIF", b"XMP "):
                found.append(tag.decode())
            i += 8 + n + (n & 1)
    elif b[:8] == b"\x89PNG\r\n\x1a\n":
        i = 8
        while i + 8 <= len(b):
            n, tag = struct.unpack(">I", b[i:i + 4])[0], b[i + 4:i + 8]
            if tag in (b"tEXt", b"iTXt", b"zTXt", b"eXIf"):
                found.append(tag.decode())
            i += 12 + n
    else:
        found.append("not WebP or PNG (JPEG carries EXIF; export WebP or PNG)")
    if found:
        print(f"check-private: {path}: metadata {', '.join(found)}; strip it before committing", file=sys.stderr)
        bad = 1
sys.exit(bad)
PY
    fail=1
  fi
fi
[ "$fail" -eq 0 ] || exit 1
echo "check-private: ${#files[@]} files clean"
