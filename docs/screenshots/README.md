# README screenshots

The web app at 1440×900, from a demo library: stub files named for [Blender Studio open movies](https://studio.blender.org/films/) (CC BY) and US public-domain films, with posters, backdrops and plots from TMDb. Accounts are `admin`, `Family` and `Guest`.

## Retake them

On a Docker host with the VCRX image built (`deploy/`):

```bash
VCRX_TMDB_API_KEY_FILE=/path/to/tmdb-key scripts/screenshots/demo.sh   # about 7 minutes the first time
```

It serves on 127.0.0.1:3790 only. From another machine, tunnel to it (`ssh -L 3790:127.0.0.1:3790 <host>`), then:

```bash
node scripts/screenshots/capture.mjs        # writes the four .webp files here
scripts/check-private.sh docs/screenshots/*.webp
scripts/screenshots/demo.sh down            # on the host: removes the container and its files
```

## Before committing a screenshot

`scripts/check-private.sh` (in CI) refuses an image here that carries metadata (EXIF, XMP, PNG text) or an address in its bytes. It cannot read the picture, so look at every image yourself and check that it shows:

- only the demo library, never a real one: a poster wall advertises what is on someone's disk;
- no real names: accounts, profiles, devices, friends' servers;
- no address bar, IP address, hostname or path;
- no Live TV page or channel list.
