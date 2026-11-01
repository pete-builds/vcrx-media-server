# Production readiness

Can VCRX replace Plex for 30 consecutive days of household use, with no critical playback failure, no unauthorized
data exposure and no unrecoverable data loss? This is the honest answer as of 2026-11-01, and what is still needed.

## Readiness score: 6 / 10

| Area | Score | Why |
|---|---|---|
| Security | 7 | Authentication, the route gate, path containment and the web app hold up under review. A few deployment-hardening items are still open; they are tracked outside this repository until they are fixed. |
| Data integrity | 7 | The library-wipe path (S1) and three partial-outage bugs are fixed. Backups are verified when taken (#87), and the restore procedure (#107) was drilled on copies of real backups. Still open: backups live on the same disk as the database. |
| Playback | 7 | 17 file shapes decided correctly for three browser profiles; TS playback and seeking fixed. Open: no real-browser rendering run over the corpus, Safari HLS never fetched, Dolby Vision untested, TV checked by hand only. |
| Operations | 4 | The deploy refuses when it cannot tell whether anyone is watching and checks the server came back on the new version. Still no rollback image, no resource limits, no tagged images, no versioned APK. |
| Tests and CI | 7 | Race-enabled Go tests with real ffmpeg, an access walk over every route, a headless Chrome smoke, gitleaks, govulncheck, shellcheck, a check that shipped migrations never change. Missing: dependency update bot, image scan, TV instrumented tests, the playback corpus in CI. |

A 6 means it can run the household day to day while Plex stays installed as the fallback, but it cannot yet be the
only server: a bad deploy has no tested way back to the previous image.

## Minimum requirements before the 30-day trial

1. ~~Merge the fixes waiting for review and deploy.~~ Done 2026-11-01: every audit fix is on `main` and deployed.
2. ~~Restore drill on a real backup.~~ Done 2026-11-01, on copies only, never the live database: the newest nightly
   backup restored with an intact database and every table's row count unchanged; a backup one schema version
   older was migrated forward at startup, copied aside first, and lost no rows.
3. **Off-host backup copy.** The nightly copy goes to the NAS as well as the config folder.
4. **Rollback.** Tag each built image with its commit and keep the previous one; document "previous tag plus the
   `pre-migrate-vN` backup" as the way back.
5. **Resource limits.** Measure one 4K transcode and one remux on the host, then set `mem_limit`, `cpus` and
   `pids_limit` with headroom.
6. **Close the open deployment-hardening items** before the trial starts.

## Measured on the host (2026-11-01 00:00 ET, read only)

- VCRX idle: 56 MiB RAM, 0.03% CPU, 20 processes. No memory or process limit set.
- Database 16 MB plus a 3.4 MB WAL; 19 backups, 235 MB, on the same filesystem.
- That root filesystem is 85% used (35 GB free) and shared with every other service; HLS segments can reach about
  1.5 GB per stream, 9 GB at six slots.
- Host: 12 cores, 31 GiB RAM.

## During the trial

Plex stays installed and running. A daily check records: crashes and restarts (`docker inspect` restart count),
playback refusals and failures from the activity log, ffmpeg processes with no session, `PRAGMA integrity_check`,
the age of the newest backup, and free disk. Any critical playback failure, any data loss, or any access outside a
user's policy restarts the 30-day count.

## Remote access and multiuser

Multiuser works (household profiles, PINs, kids limits, invited devices, friends' servers). Remote access today is
through a private VPN. NAT traversal without a vendor relay is not built. No friend's server is linked until the
federation hardening in `PLAN.md` is complete.

## Documentation gaps

- No rollback procedure beyond restoring a pre-migration backup, and no troubleshooting section for "a library
  vanished" or "a stream will not start".
