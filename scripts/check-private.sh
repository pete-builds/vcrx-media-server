#!/usr/bin/env bash
# Fails when a tracked file names a real home network or a home directory.
# This repo is public: examples use 192.168.1.x, and the real deployment's
# addresses stay in the private workspace. Run from anywhere in the repo.
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
# 192.168.0 and .1 are the documentation examples; .16 and .224 name Docker's
# default address pools. Any other 192.168 subnet is someone's real LAN.
pattern='192\.168\.([2-9]|1[0-57-9]|[2-9][0-9]|1[0-9]{2}|2[013-9][0-9]|22[0-35-9])\.|/(home|Users)/[a-z]'
if hits=$(grep -nIE "$pattern" -- "${files[@]}"); then
  echo "check-private: real network or home path in a public file:" >&2
  echo "$hits" >&2
  exit 1
fi
echo "check-private: ${#files[@]} files clean"
