# Logins for other people, and the store apps

Goal: someone who is not the author can install VCRX on their own server, get the TV and phone apps from Google Play, and sign in without a terminal. Friends watching someone else's server keep joining by invite link, which already works.

Decided 2026-11-24:

- Both audiences: friends on an existing server (invites, unchanged) and new owners running their own server.
- Google Play for the Google TV and phone apps, published under an organization developer account. No Fire TV for now: older Fire OS devices could take the same APK, and the newer Vega OS devices cannot run Android apps at all.
- Watching away from home stays free of any central service. Setup points new owners at Tailscale; the domain, port forward and Caddy route in `INTERNET-ACCESS.md` stays for experts.

## Features, in build order

Each one is a sentence an owner can check by using the app. One per session, finished end to end and exercised in a real browser or on a TV before the next starts.

1. **First-run setup.** Opening a brand-new server in a browser on its own network shows Set up, which creates the admin and lands in Settings, Libraries, signed in; from the internet, or once any account exists, it never appears. *Built 2026-11-24 (`server/internal/api/setup.go`, `scripts/setup-smoke.sh`).*
2. **Add a library in the web app.** An admin adds Movies, TV, Music or Audiobooks by choosing a folder under the mounted media, and the first scan starts; no YAML edit.
3. **The TV and phone find the server.** On a new install the apps list VCRX servers on the home network (mDNS, `_vcrx._tcp`) and connect with one press; typing an address stays as the fallback.
4. **Self-service account.** A signed-in person changes their own password and sees and signs out their own devices, in the web app and both apps.
5. **Away from home, the easy way.** Settings shows whether the server is reachable over Tailscale and walks an owner through it; the store listing and README say what works where.
6. **Store build.** CI builds signed Android App Bundles for the TV and phone apps; a privacy policy page is published; store artwork and screenshots come from the brand kit.
7. **Reviewer demo server.** A small public server holding only public-domain films, with a demo account for Google's reviewers, kept separate from any real library.
8. **Google Play.** Internal test, then closed test, then production, for both apps.

## Store notes

- One Play developer account covers Google TV and phone. A new personal account must run a closed test with at least 12 testers for 14 days before production; an organization account is exempt but needs a D-U-N-S number.
- Play App Signing: to let an existing sideloaded install update from the store without uninstalling, upload the current signing key to Play rather than letting Play generate a new one. The key never goes in this repo.
- The TV app already meets the basics Play checks for TV: Leanback launcher, no touchscreen requirement, banner, D-pad navigation. Still to verify against the current Android TV app quality guidelines before submission.
- Google's developer verification for sideloaded apps reaches TV devices in 2027; a Play developer account covers it.
