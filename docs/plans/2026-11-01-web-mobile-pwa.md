# Web app on phones, and as an installable app

Pete's brief (2026-11-01): make the web client a polished, installable,
mobile-first app that a friend can open over the future HTTPS hostname, sign
in to, browse, play and resume from an Android phone or an iPhone. Improve
what exists; no rewrite, no new frameworks, no new product features.

## Audit (main at 77512e8)

Already right, keep it:

- **Routing** is by hash (`#/item/12`), so a reload, a deep link or a home
  screen launch always lands on `index.html`; the server also answers unknown
  extensionless paths with the page.
- **Updates**: every static file is served `Cache-Control: no-cache` with a
  content-hash ETag. A deploy shows on the next launch and an unchanged file
  costs a 304. There is no service worker to go stale.
- **Session**: an HttpOnly cookie. Script never sees the token. Browser
  storage holds only a random device id (`vcrx.device`) and the last chosen
  subtitle language per profile. Every request is same-origin and relative:
  no `localhost`, LAN address or `http://` URL anywhere in the client.
- **Expired session**: any 401 sends the browser to sign in with `?next=` set
  to the page it was on.
- **Phone layout** (#115's redesign): no page is wider than a 390px screen
  (the smoke test checks), three posters across, episode list rows, a hero
  that stacks, pill Play, lazy images with srcset on heroes, skeletons in the
  page's shape, reduced motion honoured.
- **Playback** picks per browser: direct play where the browser decodes the
  file, fMP4 for converted streams, HLS for Safari, with progress beats,
  resume and seek reports.
- Music already drives lock-screen controls through the Media Session API.

What is actually wrong, in order of impact:

1. **Not installable.** No manifest, no 512px or maskable icon, no iOS
   standalone meta. "Add to Home Screen" gives a bookmark that opens Safari's
   chrome, and Android offers no install.
2. **No safe areas.** `viewport-fit` is not set, so a notched iPhone in
   landscape letterboxes the player, and in standalone the bars have no room
   for the home indicator.
3. **Phone navigation hides everything behind a hamburger.** Movies, TV and
   Settings are two taps away at the top of the screen, out of thumb reach.
4. **The phone player pauses when you tap it.** On converted streams a tap on
   the picture toggles play, and the controls hide after 3 s with only a mouse
   move to wake them, so a phone has no way to bring them back without
   pausing. No skip back or forward button exists for touch.
5. **No way back in standalone.** An installed app has no browser back
   button; on an iPhone, item and library pages have no visible way back
   except the logo.
6. **A dropped connection ends direct play.** A network error on a
   progressive stream shows "Playback failed"; only HLS retries.
7. **Resume points lag on phones.** Progress is reported every 10 s while
   playing and on `pagehide`; an iPhone that backgrounds the app and later
   kills it can lose up to the last beat. Reporting when the page is hidden
   fixes it.
8. **No lock-screen controls for video.** Music has Media Session; video
   does not, so the lock screen and headphones show nothing useful.
9. **Rotating to watch.** Full screen on Android stays portrait; Chrome
   allows a landscape lock while full screen.
10. **Small controls on touch.** `btn-small` in the player is 32px tall, the
    converted-stream buttons about 34px, under the 44px touch guideline. The
    library filters take three rows above the first poster on a phone.

## Plan (one PR, `web/mobile-pwa`)

1. `manifest.webmanifest` served as `manifest.json` (Go's MIME table has no
   `.webmanifest` and the server sends `nosniff`), icons 192/512 (any), 512
   maskable, 180 apple-touch, all generated from `brand/icons` by
   `brand/tools/make-assets.py`. iOS: `apple-mobile-web-app-capable`,
   `mobile-web-app-capable`, status bar `black`, title. **No service worker**:
   Chrome no longer needs one to install, and the brief allows none; it
   avoids any chance of caching authenticated data or serving a stale app.
2. `viewport-fit=cover` and `env(safe-area-inset-*)` padding on the top bar,
   the new tab bar, the page, the player bars, toasts and the music bar.
3. A bottom tab bar under 720px: Home, the first movie and show libraries,
   Search, and More (the existing menu as a sheet: other libraries, Live TV,
   Activity, Settings). The top bar on a phone keeps the wordmark and the
   profile button; Search gets its own page input.
4. Player: touch wakes the controls first (a tap on a hidden UI only shows
   it); back 10 s / forward 10 s buttons (30 s on converted streams, as the
   keys do); 44px touch targets under `pointer: coarse`.
5. A back button in the top bar on every page but Home when running
   standalone or on a phone.
6. Progressive streams reconnect up to 3 times at the last position on a
   network error, with "Reconnecting" shown.
7. Report progress on `visibilitychange` to hidden (keepalive).
8. Media Session for video: title, artwork, play, pause, seek back, seek
   forward, seek to, position state; cleared when the player closes.
9. Android: lock landscape while full screen when the video is wider than
   tall; unlock on exit.
10. Library filters scroll in one row on a phone.

## Checked in a browser, not on a device

`scripts/web-smoke.mjs` (headless Chrome, emulated 390x844 touch phone) now
also fails on: no manifest, a manifest that is not standalone or whose start
page is out of scope, an icon that is missing or not the size it claims, no
192, 512 or maskable icon, no Apple touch icon, no `viewport-fit=cover`, no
tab bar, more than five tabs, a tab under 44px, Home not marked current, the
More sheet showing before it is asked for or staying open after a choice, no
Back on a phone page or Back not returning, and converted-stream controls
not named for what a press does (Play/Pause, Mute/Unmute) or missing skip
back and forward. Control: with the manifest link renamed and More removed it
failed three ways by name.

## On a real phone (cannot be emulated; do these on the HTTPS hostname)

iPhone, Safari:

- [ ] Share, Add to Home Screen: the icon is the black VCRX tile, the name
      "VCRX"; it opens with no Safari bars and a black status bar.
- [ ] Sign in, close the app from the switcher, open it again: still signed in.
- [ ] Tab bar sits above the home indicator; nothing hides under the notch
      in landscape.
- [ ] Play a direct-play movie: native controls; rotate to landscape and full
      screen; seek; pause; lock the phone: the lock screen shows the title,
      poster, play/pause and skip.
- [ ] Play a converted title (HLS): it plays; skip back/forward 30 s; a tap
      shows and hides the controls without pausing.
- [ ] Choose subtitles; they show in and out of full screen.
- [ ] Mid-movie, swipe the app away; reopen, open the title: Resume is within
      a few seconds of where it stopped.
- [ ] Turn on airplane mode for 10 s mid-play, then off: playback resumes at
      the same spot (direct play), or the error offers Back (converted).
- [ ] Open a shared `#/item/<id>` link: the page opens; Back goes Home.

Android, Chrome:

- [ ] Chrome offers Install (menu, Install app); the icon is the maskable
      black tile, not a white-ringed one.
- [ ] Full screen on a converted title turns the phone to landscape;
      leaving full screen lets it turn back.
- [ ] The system Back gesture steps back through pages and never out of the
      app from Home.
- [ ] Notification shade shows the playing title with controls.
- [ ] Sign out (More, Settings): the app shows sign-in or the picker, and
      reopening it does not sign back in on the public hostname.

## Limitations and follow-ups

- **No offline use** (by design and by the brief): no service worker, so a
  launch with no network shows "VCRX is not answering".
- **iPhone full screen** is the video element's own (iOS gives no element
  full screen), so VCRX's skip and subtitle controls are hidden there; iOS's
  own controls take over.
- **Audio track choice** works only on converted streams; a browser plays a
  file's default track. Unchanged.
- **Picture subtitles** (PGS, VobSub) still need the TV app (P10b).
- **Remote access** is browser-ready: every URL is relative, the session is an
  HttpOnly cookie, nothing sensitive is in storage. The server side (Secure
  cookies behind TLS, trusted proxies, auto sign-in never applying through a
  proxy) is the security lane's PR #129.

## Not changed

Server code (none needed: see the security lane, `sec/internet-exposure`,
for cookies and proxy headers), routes, APIs, the TV app, the playback engine.
