# Developing VCRX

Building and testing the server and the web app. The TV app's build is in [tv/README.md](../tv/README.md).

## Server and web app

The server is Go 1.26 with a pure-Go SQLite driver; no cgo.

```bash
cd server
go test -race ./...      # includes the OpenAPI drift test
go vet ./...
golangci-lint run
govulncheck ./...
```

To work on the web app or the API against a few generated test clips: `./scripts/dev-server.sh`, then open http://127.0.0.1:3751/. It builds the clips with ffmpeg on first run, serves the web files from `server/web/static` so an edit shows on reload, and signs the local browser in. `server/web/check-js.sh` is the web app's compile check, and `node scripts/web-smoke.mjs` drives every page (at desktop and phone width), all three playback paths (direct, audio converted, video transcoded, and HLS for Safari), Continue Watching removal, search as you type, the shortcuts list, Home's rows and Customize Home in headless Chrome against the dev server; CI runs both. The dev server is a development build (`-tags vcrxdev`): it pins Home's date to `VCRX_DEV_HOME_DATE` (default 2026-10-31, so the Halloween row shows whatever the day) and seeds a play history with `vcrx dev-seed`. Neither exists in a normal build or the image.

`VCRX_HOME_PERF=1 go test ./internal/catalog -run HomePerformance -v` times Home on a generated library of 5,000 films, 500 shows and 20,000 plays.

On NixOS, run `nix-shell` at the repo root first: [shell.nix](../shell.nix) provides these tools, plus the Docker client and adb for the TV app. The API contract is [server/api/openapi.yaml](../server/api/openapi.yaml), served at `/api/v1/openapi.yaml`; a test fails when it and the registered routes disagree. Migrations are forward-only SQL files in `server/internal/db/migrations`.

Design notes and the verified facts the build relies on: [REFERENCE.md](REFERENCE.md).

## Brand

The VCRX wordmark, split V mark, app icons and TV tile live in [brand/](../brand/README.md): VCRX Green `#008F11` on Black, Rajdhani for display type and Inter for interface text. The kit is raster artwork, so the logo is always the image, never retyped in a font. `brand/tools/make-assets.py` derives the Android launcher icons, TV banners, in-app logo files and the web app's icons from it; rerun it after changing the kit.
