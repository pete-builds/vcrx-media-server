# Production readiness audit and hardening

Status: approved by Pete 2026-10-31 23:20 ET. Runs autonomously overnight, ending 09:00 ET 2026-11-01.
Goal: VCRX can replace Plex for 30 consecutive days of household use with no critical playback failure, no unauthorized data exposure, and no unrecoverable data loss.

## Ground rules

- All work happens in its own worktree (`~/vcrx-audit`), on its own branches. The Live TV build (`~/vcrx-live`, `docs/plans/2026-10-31-live-tv.md`) is a separate lane.
- Migration numbers are contiguous and the loader rejects gaps. Before adding a migration, check origin/main and open PRs for the next free number.
- **No deploys to nix1 and no TV installs from this effort** without Pete's explicit go-ahead. This overrides the standing deploy rule for this work only. No changes to live libraries or the live database.
- PRs: one fix per PR, each with its regression test. Merge when every check is green (standing merge rule). Never merge red.
- Never change public API contracts, auth semantics, database behavior, or user-facing behavior silently. Any such change is called out in the PR body and in `docs/SECURITY-AUDIT.md`.
- No new features unless a confirmed defect needs one. No new dependencies without a stated reason.
- Generated code is not validated functionality. A playback configuration passes only when a test actually ran it.

## Evidence from the first look (2026-10-31)

- About 60k lines of Go across 36 internal packages, 94 Go test files; 115 Kotlin files, 30 tests; migrations to 0016.
- CI: go vet, `go test -race`, govulncheck, `web/check-js.sh`, `deploy_test.sh`, Docker build, headless Chrome smoke (`scripts/web-smoke.mjs`). No gosec or CodeQL job, no Firefox or Safari check, no committed media fixtures.
- Release: image is `vcrx-media-server:local` built on nix1; no version tags, no registry image; the TV APK is signed with a throwaway key on nix1.
- `deploy/compose.yaml` is hardened (read_only, cap_drop ALL, non-root) but sets no memory, CPU, or pids limits.
- Auth on nix1 leans on `trusted_networks` auto sign-in (LAN, IoT VLAN, three Tailscale IPs, all admin). `clientIP` in `internal/api/middleware.go` decides that boundary.
- Federation tokens are not yet encrypted at rest (known follow-up, required before any friend is linked).

## Expected failure modes (hypotheses to prove or disprove)

1. Scanner hides or deletes the library when the media mount is missing or empty.
2. ffmpeg (HLS, remux, audio) outlives a disconnected or seeking client.
3. Backups copy a live SQLite file instead of using the backup API, and restore was never exercised.
4. `clientIP` can be spoofed through X-Forwarded-For, or Docker/Tailscale addresses fall inside a trusted prefix.
5. The access route-walk test proves routes are listed, not that they refuse access.
6. Subtitle, artwork, pre-roll, or extras paths bypass `internal/fsafe` and follow symlinks out of the library.

## Phases

### 0. Isolation
Worktree, branch naming (`audit/*`, `fix/*`, `docs/*`), migration coordination with the Live TV lane.

### 1. Audit (read-only)
- Security: `claude-security` scan of `server/`.
- Direct reading: playback and remux, scanner and library store, db and migrations and backup, auth/household/invites/federation, deploy and config, TV playback lifecycle (Media3), web app.
- Every finding records file:line, severity, evidence, consequence, remediation, and either a reproduction or "unconfirmed".

### 2. Security and reliability proofs
Each P0/P1 hypothesis gets a failing test first: mount-loss scan, killed-client ffmpeg reaping, backup and restore round trip, forged-header and prefix-boundary auth tests, symlink escapes. Every clean result carries a control proving the check can fail.

### 3. Playback matrix
- Generated corpus (ffmpeg lavfi): H.264, HEVC, AV1 in MKV, MP4, TS; AAC, AC-3, E-AC-3, DTS, TrueHD, stereo, 5.1, 7.1; HDR10 tagged; SRT, ASS, PGS, external and embedded.
- Automated: decision (direct play, remux, audio convert, transcode), ffprobe of output, seek, resume, markers, concurrent streams, interrupted streams. Chrome headless in CI; Firefox headless if feasible.
- Hardware-only, marked as such: Safari (Pete's Mac), Google TV (adb, only when the TV is idle), Dolby Vision (needs a real sample).

### 4. Production readiness
Tagged image and versioned APK release pipeline with a real signing key, compose resource limits, restore drill, rollback procedure (forward-only migrations mean rollback is a DB restore), measured CPU, RAM, disk at steady state.

### 5. Fixes
P0 (security, data loss, critical playback), then high-value P1. Each: reproduce, regression test, smallest fix, run tests, document result and limits.

### 6. 30-day soak
Starts only after P0 fixes are deployed with Pete's go-ahead. Plex stays up as fallback. A daily health check logs crashes, failed plays, ffmpeg process count, `PRAGMA integrity_check`, backup freshness.

## Deliverables
`docs/PRODUCTION-READINESS.md`, `docs/SECURITY-AUDIT.md`, `docs/PLAYBACK-COMPATIBILITY.md`, `docs/RELEASE-CHECKLIST.md`, plus a morning summary: readiness score, confirmed defects, test results, priorities, release blockers, and everything that needs Pete (deploys, hardware checks, decisions).

## Progress log
Each loop iteration appends one line here or in the PR it opened.
