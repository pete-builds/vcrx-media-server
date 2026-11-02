# Release checklist

For every server deploy and every TV APK. A box is ticked only when the step ran, not when it should have.

## Before merging to main

- [ ] Every check on the PR passed: `test` (race detector, OpenAPI drift, access walk), `web` (headless Chrome
      smoke), `image`, `gitleaks`, and `tv` when the TV app changed.
- [ ] A fix carries the regression test that fails without it, and the PR names the control.
- [ ] A new route has its probes in the access walk (404 probe, list probe, or a reasoned exemption).
- [ ] A new migration has the next free number on `main` and among open PRs (the loader refuses gaps).
- [ ] Public API, database behaviour, authentication or anything a user sees changed only on purpose, and the PR
      says so.

## Before deploying the server

- [ ] Nothing is playing (`deploy.sh` refuses; if its check cannot run it now refuses too, and `--force` is a
      deliberate choice).
- [ ] The release is a tag on `main` (`git tag vX.Y.Z && git push origin vX.Y.Z`) and its `release` workflow
      passed: the image is on ghcr.io, labelled with the version, with an SBOM and build provenance.
- [ ] The version running now is noted, so there is something to go back to.
- [ ] A migration is coming: the pre-migration backup will be written to `data/config/backups`; there is free space
      for another copy of the database.
- [ ] Config keys added by this release go into `vcrx.yaml` only after the new binary is running (the parser
      refuses unknown keys, so the old binary would not start).

## After deploying the server

- [ ] The served version equals the commit just built (`/api/v1/server`).
- [ ] `/readyz` answers ok (it touches the database; `/healthz` does not).
- [ ] `scripts/smoke-api.sh` passes against the live server.
- [ ] The logs since start show no `converted stream failed`, no migration error, no scan aborted unexpectedly.

## Rolling back

1. Stop the container.
2. Copy `data/config/backups/vcrx-pre-migrate-v<N>-*.db` over `data/config/vcrx.db` (N is the schema version before
   the deploy) and delete the `-wal` and `-shm` files beside it.
3. `deploy/deploy.sh --release <previous version>` (it pulls that image and records it in `.env`).
4. Check the served version and the smoke test, as above.

Step 2 was drilled on 2026-11-01 against copies of real backups, never the live database: a pre-migration backup
one schema version older started, migrated forward and kept every row. Step 3 needs two published releases
before it can be drilled.

## TV APK

- [ ] Built from `main` with `tv/build-apk.sh`, signed with the release key (not a throwaway key).
- [ ] `versionCode` is higher than the installed one (Android refuses a downgrade).
- [ ] Installed only when the TV is idle; nobody is watching.
- [ ] On the TV: sign in or stay signed in, play one direct and one converted title, seek, resume, captions, Back
      from the player, music stops on Home.

## Builds still to make reproducible

- ffmpeg in the image is Debian's, unpinned: a rebuild of the same commit can change playback behaviour.
- A host that runs `deploy.sh` without `--release` still builds its own image; releases are built once in CI.
- The released image is linux/amd64 only.
