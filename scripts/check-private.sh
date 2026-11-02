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
[ "$fail" -eq 0 ] || exit 1
echo "check-private: ${#files[@]} files clean"
