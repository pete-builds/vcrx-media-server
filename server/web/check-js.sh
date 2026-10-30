#!/usr/bin/env bash
# Syntax-checks the web client, which has no build step, so this is its compile.
# Run from server/: web/check-js.sh
#
# Every file is parsed as an ES module from stdin. `node --check file.js` is not
# used: on Node 20 it exits 0 for a .js file with module syntax even when the
# file is broken, so it would pass anything. The control below proves this
# check still fails on bad code.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

if printf 'export const broken = ;\n' | node --input-type=module --check 2> /dev/null; then
  echo "check-js: control failed: a broken module parsed cleanly, so this check proves nothing" >&2
  exit 1
fi

status=0
count=0
while IFS= read -r -d '' f; do
  count=$((count + 1))
  if ! node --input-type=module --check < "$f" 2> "${TMPDIR:-/tmp}/check-js.err"; then
    echo "check-js: $f" >&2
    sed 's/^/  /' "${TMPDIR:-/tmp}/check-js.err" >&2
    status=1
  fi
done < <(find static/js -name '*.js' -print0)
[ "$count" -gt 0 ] || { echo "check-js: no files found" >&2; exit 1; }
echo "check-js: $count files parsed"
exit "$status"
