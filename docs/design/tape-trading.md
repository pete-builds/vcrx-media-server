# Tape trading: what may be copied between servers, and how VCRX proves it

Written 2026-10-31. **Status: design draft for security review. No code.** This covers milestone 10 item 2 in `docs/PLAN.md`, "Legal tape trading". Pete approved starting design work on 2026-10-31. Replication code waits until this design has been security-reviewed and Pete has signed off on it, because it is the only exception to milestone 9's rule: stream to invited peers, never copy files between servers.

External facts were read live on 2026-10-31 and are cited inline. Where a fact about archive.org came from a live API response rather than a documentation page, the response URL is given so a reviewer can repeat the read.

## 1. The rule

A file may be copied to a peer only when VCRX holds a **verified provenance record** for that exact file, identified by its SHA-256, on one of three bases:

1. **Published by the Live Music Archive:** archive.org offers this exact file for public download in a band's LMA collection.
2. **Published under an open licence:** a trusted host publishes this exact file under a Creative Commons licence or a public-domain tool.
3. **My own recording:** an attested home video, in the limited form in section 2.4, which never travels more than one hop.

Everything else stays stream-only, exactly as milestone 9 says. Shareability belongs to a file, never to a title: a 4K copy and a 1080p copy of the same item each need their own record. There is no free-form "shareable" toggle anywhere, in the API, the CLI, the web app or the database.

## 2. What makes a file shareable, provably

### 2.1 Common ground: hashing

VCRX hashes nothing whole today; `media_versions` stores size and mtime only (`server/internal/db/migrations/0001_init.sql`). This design adds two kinds of hash:

- **Full hashes,** computed on request for a candidate file in one read: SHA-256 (VCRX's own identity for the file and the only hash peers exchange), plus SHA-1 and MD5, because those are what the external sources publish. The size and mtime at hashing time are stored with them; if either changes, every record bound to the file is void until it is re-hashed.
- **A quick fingerprint** for every file VCRX scans in any library: size plus SHA-256 of the first and last MiB. It is cheap (2 MiB read per file) and feeds the ever-seen index in section 2.4.

Full hashing of a whole movie library is never done; only files an admin puts forward as candidates get hashed.

### 2.2 Basis 1: the band's taping policy, through the Live Music Archive

**What archive.org exposes.** The Live Music Archive is the archive.org collection `etree` (https://archive.org/metadata/etree). Each band has its own collection inside it, and each show is an item whose `collection` field lists both, for example `["UmphreysMcGee", "etree"]` (https://archive.org/metadata/UM2009-04-04.flac16). The Metadata Read API returns, for any item, the metadata plus a `files` array whose entries carry `md5`, `sha1`, `crc32`, `size`, `format` and `source` (https://archive.org/developers/md-read.html). The metadata schema says `collection` is editable by a user admin, while `licenseurl` and `rights` are set by the uploader (https://archive.org/developers/metadata-schema/index.html). So collection membership is archive.org's own statement, and the licence fields are the uploader's claim.

**There is no machine-readable trade policy.** archive.org records each band's permission as free text in the `rights` field of the band's collection; for Umphrey's McGee it is a quoted 2002 permission email (https://archive.org/metadata/UmphreysMcGee). The LMA help page says the same: archive.org quotes "the band's wishes" in the Rights section, and restrictions such as "no soundboard recordings" or a single excluded show are possible (https://help.archive.org/help/live-music-archive-etree-org/). VCRX must not parse that prose. Instead it relies on archive.org's enforcement of it, which is visible in structured fields:

- Items an artist restricts sit in a `stream_only` collection, and their original files carry `"private": "true"`. A Grateful Dead soundboard shows both: `collection` includes `stream_only`, and its `.flac` originals are private (https://archive.org/metadata/gd1981-05-12.123259.sbd.miller.flac16). The help page notes such shows "may be available only as streaming".
- The Grateful Dead collection's `rights` field opens with a "Limited Flag" list and says specific shows may become unavailable by rights-holder request (https://archive.org/metadata/GratefulDead/metadata/rights).
- Policy changes have removed whole bands before: Dave Matthews Band shows came down in 2003 days after the band changed its policy (https://www.nancies.org/news/2003/01/internet-archive-removes-dmb-shows/).

**The video problem, stated plainly.** LMA permission is permission for audio. The help page says video uploads to the LMA are "not recommended nor supported" and points concert video elsewhere (same page), and archive.org's search finds 149 `movies` items in `etree` against 296,607 items in total (https://archive.org/advancedsearch.php?q=collection%3Aetree+AND+mediatype%3Amovies&fl%5B%5D=identifier&rows=0&output=json, run 2026-10-31). Trade-friendly bands often ban video outright: Umphrey's McGee "encourages the audio taping and trading" of its shows, and says video taping is "strongly forbidden" (https://umphreys.com/taping-photography-policy). **So a band's audio taping policy never makes a concert video shareable.** VCRX libraries today hold only movies and shows, so this basis covers very little of what VCRX holds. That is the honest answer, and the design keeps the basis narrow rather than stretching it.

**What basis 1 means, therefore:** archive.org publicly offers this exact file for download, in a band collection inside `etree`. That claim is checkable and does not depend on reading anyone's prose.

**Evidence VCRX stores:** the item identifier and file name; the raw JSON of the metadata response, its SHA-256, and when it was fetched; the matched `md5`, `sha1` and `size`; the item's `publicdate`; the band collection name.

**Verification, all steps required:**

1. The admin names the source: an archive.org identifier and a file name. VCRX never guesses a match from a band name, a date or a filename.
2. VCRX fetches `https://archive.org/metadata/{identifier}` itself, over TLS with the system roots, from a fixed host. There is no configurable mirror.
3. The item's `collection` contains `etree` and exactly one band collection, and does **not** contain `stream_only`. The response has no `is_dark` flag and is not empty.
4. The named file is in `files`, is not marked `private`, and its `size`, `md5` and `sha1` all equal VCRX's own full hashes of the local file. A file entry missing either hash does not verify.
5. The item has been public for at least 30 days (`publicdate`), so archive.org's normal takedown path has had time to act on a bad upload.
6. Any other shape of response, any network error, or any field VCRX does not recognise in those positions: not verified.

**Taper checksum files.** Tapers ship `.md5` and `.ffp` files; archive.org lists them as formats "Checksums" and "Flac FingerPrint" (the Grateful Dead item above), and item metadata may carry a taper's `md5s` field. The LMA says such files are "not required or verified" (help page above). An ffp is the MD5 of the decoded audio stored in the FLAC STREAMINFO block (RFC 9639, https://www.rfc-editor.org/rfc/rfc9639.txt), so it survives a re-encode at a different compression level, which makes it good for **identifying** which LMA item a local FLAC came from. It is never enough to **authorize** a copy: FLAC also carries APPLICATION, PICTURE and PADDING metadata blocks (RFC 9639, section 8), so a file can match an ffp exactly while carrying anything at all in those blocks. Authorization needs the whole-file `md5` and `sha1` published by archive.org.

### 2.3 Basis 2: Creative Commons and public-domain licences

**What proves a licence.** Strictly, nothing a program can check proves that the person who applied a licence had the right to. What VCRX can prove is narrower and still useful: a named, trusted host publishes this exact file under that licence. VCRX accepts two hosts:

- **Wikimedia Commons.** The MediaWiki API returns a file's `sha1`, `size` and licence (`extmetadata` `LicenseShortName` and `LicenseUrl`) in one call (https://www.mediawiki.org/wiki/API:Imageinfo). For example, Big Buck Bunny 4K comes back as CC BY 3.0 with its SHA-1 (https://commons.wikimedia.org/w/api.php?action=query&titles=File:Big_Buck_Bunny_4K.webm&prop=imageinfo&iiprop=sha1|size|extmetadata&format=json). Commons accepts only freely licensed or public-domain files and deletes those that turn out not to be (https://commons.wikimedia.org/wiki/Commons:Licensing), which is why it is accepted. It publishes SHA-1 only, so a match is SHA-1 plus size.
- **archive.org,** for items with a `licenseurl`. 1,639 items in `etree` alone carry one, for example CC BY-NC-ND 4.0 and the Public Domain Mark (https://archive.org/advancedsearch.php?q=collection%3Aetree+AND+licenseurl%3A*&fl%5B%5D=identifier&fl%5B%5D=licenseurl&rows=3&output=json). Because `licenseurl` is uploader-set (metadata schema above), it is weaker evidence than collection membership. VCRX accepts it only from an allowlist of collections Pete approves (open question 2), with the same 30 day age rule and MD5 plus SHA-1 plus size match as basis 1.

**Accepted licences:** CC BY, BY-SA, BY-NC, BY-NC-SA, BY-ND and BY-NC-ND at versions 3.0 and 4.0, CC0, and the Public Domain Mark, matched against an exact list of canonical URLs. All of them allow verbatim noncommercial copying. Their duties carry over:

- **Attribution:** the licence URL, the author and the source page are stored with the file and travel with it to every peer, and the web app and TV app show them on the details page.
- **NonCommercial:** VCRX charges nothing. Any future paid feature must exclude NC files.
- **NoDerivatives:** VCRX replicates the file's bytes unchanged, never a converted copy. CC 4.0 also allows "technical modifications" needed to use a work in other formats (https://creativecommons.org/licenses/by-nc-nd/4.0/legalcode.en, section 2(a)(4)), so converting for playback is fine, but the copy that travels is always the original.

**Never accepted as evidence:** a `LICENSE` file beside the media, an NFO tag, container metadata, a licence URL typed in by an admin without a matching host record, or a claim from a peer.

### 2.4 Basis 3: "my own recording"

**The honest answer: no software check can prove someone recorded a file.** Container metadata (camera make and model, creation time, GPS) is rewritten with one ffmpeg command, and a re-encode changes every hash. So this design does not claim to verify authorship. It proposes a limited form that closes the specific loophole raised in review, makes casual misuse fail, puts a named person's attestation on every file, and caps how far such a file can travel.

**The limited form, all parts required:**

1. **A home-video library kind, fixed at creation.** A new `libraries.kind` value, `home_video`. The kind cannot be changed after the library is created, so an existing Movies library cannot be relabelled.
2. **The ever-seen index.** The quick fingerprint of every file VCRX has ever scanned, in any library, kept forever with the kind of the first library it was seen in. A file whose fingerprint was ever seen in a `movie` or `show` library is permanently ineligible. **This closes the review loophole directly:** an admin who moves or copies a file from Movies into Home Videos gets the same fingerprint, and it is refused. It does not stop a file that never passed through another library, or one re-encoded first; the next steps address those.
3. **Hash registered at first scan.** The full SHA-256 is recorded when the file is first scanned into the home-video library. The attestation binds to that hash, and any later change to the file voids it.
4. **Tripwires that refuse, never approve.** A file is ineligible if any of these hold: its parsed name matches a TMDb title (VCRX already matches titles, `server/internal/tmdb/`); its name carries release-group or scene naming; it has Dolby Vision or HDR10 mastering metadata; it has TrueHD, DTS-HD or Atmos audio; it has two or more dubbed audio languages. Each refusal records the reason. These catch a lazy re-encode, not a careful one, and the design says so.
5. **A written, per-file attestation.** The admin signs a fixed statement for each file, not a batch: they recorded it themselves, or hold the rights holder's permission, and accept responsibility for sharing it. VCRX signs the statement, the file's SHA-256 and the admin's user id with the server's key (the server identity milestone 9 introduces). The admin's name is shown to every recipient.
6. **One hop only.** A home video goes only to direct peers whose admin has opted in to receiving home videos from this server. A receiving server can stream it to its own users but can never pass the file on, and its own attestation is impossible because the file arrived from a peer, not a first scan.
7. **Caps.** A per-peer limit on attested bytes per week (default 20 GB, open question 4), so the path is useless for moving a film collection.
8. **Every step audited** (section 3.5): the scan that registered the hash, each refusal and why, the attestation, every transfer and every revocation.

**The trade-off.** This stops the accident and the lazy loophole: moving a movie into Home Videos, mislabelling a library, sharing a renamed rip. It does not stop a determined admin who re-encodes a film, strips its metadata, renames it and signs a false attestation. Against that admin, the design gives attribution (a signed, named statement on the recipient's server), containment (one hop, opt-in recipients, a weekly cap) and evidence (an audit chain friends hold copies of). That person can also do the same thing with any file-sharing tool, so VCRX adds no capability they lack. The alternative is to keep home videos stream-only, which milestone 9 already allows. **Recommendation:** build basis 3 last, after bases 1 and 2 have run for a while, and only if streaming home videos proves not enough (open question 3).

## 3. Data model

A sketch for a future forward-only migration. Names may change in review; the shape should not.

### 3.1 Hashes and the ever-seen index

```sql
CREATE TABLE file_hashes (
    version_id   INTEGER PRIMARY KEY REFERENCES media_versions(id) ON DELETE CASCADE,
    sha256       TEXT NOT NULL,
    sha1         TEXT NOT NULL,
    md5          TEXT NOT NULL,
    size         INTEGER NOT NULL,
    mtime        INTEGER NOT NULL,      -- must still match media_versions, or the hash is stale
    hashed_at    INTEGER NOT NULL
);
CREATE INDEX file_hashes_sha256 ON file_hashes (sha256);

-- Never deleted, never updated after insert.
CREATE TABLE seen_fingerprints (
    quick_fp           TEXT PRIMARY KEY,  -- size plus SHA-256 of first and last MiB
    first_library_kind TEXT NOT NULL,
    first_library_id   INTEGER,
    first_seen_at      INTEGER NOT NULL
);
```

### 3.2 Provenance records

```sql
CREATE TABLE provenance_records (
    id             INTEGER PRIMARY KEY,
    sha256         TEXT NOT NULL,          -- the record binds to bytes, not to a version row
    version_id     INTEGER REFERENCES media_versions(id) ON DELETE SET NULL,
    basis          TEXT NOT NULL CHECK (basis IN ('lma', 'open_licence', 'own_recording')),
    state          TEXT NOT NULL CHECK (state IN ('pending', 'verified', 'failed', 'expired', 'revoked')),
    origin         TEXT NOT NULL CHECK (origin IN ('local', 'peer')),
    origin_peer_id INTEGER,                -- set when the file arrived from a peer
    verified_at    INTEGER,
    next_check_at  INTEGER,
    revoked_at     INTEGER,
    state_reason   TEXT,
    created_by     INTEGER REFERENCES users(id),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX provenance_sha256 ON provenance_records (sha256, state);
```

### 3.3 Evidence

```sql
CREATE TABLE provenance_evidence (
    id              INTEGER PRIMARY KEY,
    record_id       INTEGER NOT NULL REFERENCES provenance_records(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN
                      ('ia_item', 'commons_file', 'attestation', 'tripwire_pass', 'first_scan')),
    source_url      TEXT,                  -- the exact URL fetched
    fetched_at      INTEGER NOT NULL,
    response_sha256 TEXT,                  -- hash of the raw response body
    response_body   BLOB,                  -- the raw body, compressed
    matched         TEXT NOT NULL,         -- JSON: which hashes and fields matched
    licence_url     TEXT,                  -- canonical URL from the allowlist, basis 2 only
    attribution     TEXT,                  -- JSON: author, title, source page
    signature       BLOB                   -- server signature, attestations only
);
```

Evidence rows are never edited. A recheck adds a new row.

### 3.4 Verification state and revocation

```
pending ──verify ok──▶ verified ──recheck ok──▶ verified
   │                      │  │
   └──verify fails──▶ failed │  └──recheck overdue or unreachable──▶ expired ──recheck ok──▶ verified
                             └──source withdrawn──▶ revoked   (terminal: a new record is needed)
```

- **Rechecks.** Basis 1 and 2 records are rechecked every 24 hours and again immediately before every outbound transfer. A record whose `next_check_at` has passed is `expired`, which is not shareable. With no internet, records expire and trading stops; streaming and the rest of VCRX keep working, as REFERENCE.md's offline rule requires.
- **What counts as withdrawn:** the item or file is gone, dark, empty or private; the item joined `stream_only`; it left its band collection; the published hashes changed; the licence URL changed to one not on the allowlist; the Commons file was deleted. For basis 3: the admin withdraws the attestation, or the file changed.
- **A permissive flip is never automatic.** If a recheck finds a source that was restrictive is now permissive, the record does not move to verified on its own; an admin must start a new verification and the 30 day age rule applies from the change.
- **Revocation reaches copies.** Each server holding a copy runs its own rechecks, so a band's policy change revokes every copy independently, with no message needed from the sender. For basis 3, the originating server sends a signed revocation to the peers it sent the file to. A revoked copy is no longer streamed or offered, and is queued for deletion from the trade store (open question 5 sets the grace period).

### 3.5 Audit trail

```sql
CREATE TABLE provenance_events (
    id          INTEGER PRIMARY KEY,
    record_id   INTEGER REFERENCES provenance_records(id),
    at          INTEGER NOT NULL,
    actor       TEXT NOT NULL,           -- 'user:<id>', 'system', 'peer:<id>'
    action      TEXT NOT NULL,           -- hashed, verified, refused, attested, rechecked,
                                         -- expired, revoked, offered, sent, received, rejected, deleted
    from_state  TEXT,
    to_state    TEXT,
    detail      TEXT NOT NULL,           -- JSON, including the reason for every refusal
    prev_hash   TEXT NOT NULL,
    entry_hash  TEXT NOT NULL            -- SHA-256 over prev_hash and this row
);

CREATE TABLE peer_transfers (
    id          INTEGER PRIMARY KEY,
    record_id   INTEGER NOT NULL REFERENCES provenance_records(id),
    peer_id     INTEGER NOT NULL,
    direction   TEXT NOT NULL CHECK (direction IN ('out', 'in')),
    sha256      TEXT NOT NULL,
    bytes       INTEGER,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    result      TEXT NOT NULL,           -- ok, refused, aborted, hash_mismatch
    reason      TEXT
);
```

The events table is append-only and hash-chained. Every transfer manifest carries the sender's current chain head, and the receiver stores it. An admin with database access can still rewrite their own chain, but the rewrite would contradict heads already held by friends' servers, so it is detectable after the fact.

## 4. Enforcement

### 4.1 One gate

A new package, `server/internal/trade`, owns one function every copy path goes through:

```
Authorize(ctx, peer, sha256) (Grant, error)
```

It loads the verified record for that hash, rechecks it live for bases 1 and 2, checks the peer's token scope, the one-hop rule and the caps for basis 3, and confirms the local file's size and mtime still match its hashes. It returns a `Grant` bound to that peer, that hash and a short expiry. `Grant.Open()` is the **only** code in the server allowed to open a file for a whole-file peer response; it is unexported elsewhere, so a handler cannot reach the file without a grant.

### 4.2 Where provenance is checked

1. **At the moment bytes leave** (the security boundary). The trade route is registered in the route table wrapped in `requireProvenance`, which calls `Authorize` before the handler runs. The handler streams through `Grant.Open()`, computing SHA-256 as it goes, and checks size and mtime again at the end; any difference aborts the transfer and marks the record for re-hashing.
2. **At the moment bytes arrive.** The receiving server never trusts the sender's record or evidence. It writes the incoming file to a quarantine directory in a separate writable trade root (`VCRX_TRADE_ROOT`, never one of the read-only media roots, keeping REFERENCE.md's "never write to source media"), hashes it, and for bases 1 and 2 fetches the source record itself and runs the full verification from section 2. Only then does it move the file into the trade library and create its own record with `origin = 'peer'`. On any mismatch the file is deleted and the transfer logged as `rejected`.
3. **When offering.** A peer's list of tradeable files shows only verified records. This is a convenience, not a guard: the checks above are the guards.

### 4.3 The route test

Milestone 9 already requires a test over the registered routes that fails if any peer-facing route serves a whole file for storage. This design extends it; it does not replace it. The route struct gains two fields: a `peer` access level and a `delivery` value of `none`, `playback` or `trade`.

The test fails if any of these hold:

1. A route with `peer` access has `delivery: trade` but is not wrapped by `requireProvenance` (checked by a sentinel type on the wrapped handler, so a hand-written look-alike does not pass).
2. More than one route has `delivery: trade`.
3. Run live against fixtures, any peer route returns a whole file for a file whose record is missing, `pending`, `failed`, `expired`, `revoked`, or `verified` but with a changed mtime. "Whole file" is milestone 9's definition: a full-file response, a download disposition, or a range covering the whole file outside a playback session. Each route is tried with no Range header, `Range: bytes=0-`, and a range equal to the file size.
4. The `verified` fixture is refused by every peer route except the trade route.

**Control:** a test that registers an extra peer route serving a whole file without the wrapper, and asserts the checker reports it. Without that control, a broken checker and a clean route table look the same.

### 4.4 Fail closed

Every one of these refuses the copy, logs why, and leaves streaming untouched: trading switched off (`VCRX_TRADING`, off by default); no record; any state but `verified`; a recheck that cannot complete for any reason, including archive.org or Commons being down, a timeout, a TLS error or an unexpected response shape; a hash, size or mtime mismatch; a licence URL not on the allowlist; a basis value the code does not know; a peer token without trade scope; a basis 3 file arriving from a peer and offered onward; a cap reached; any error from the database. There is no override flag.

### 4.5 Recommended first version: trade the reference, not the bytes

For bases 1 and 2 the file is, by definition, already publicly downloadable from archive.org or Commons. The smallest safe first version therefore sends a peer only the **reference** (identifier, file name and hashes), and the receiving server downloads from the source itself, verifying as in section 4.2. No third-party file moves between VCRX servers at all, which keeps milestone 9's no-copy line literally true for bases 1 and 2. Peer-to-peer byte transfer, the torrent model, can follow as an accelerator that is still verified against the source hash. The cost: no benefit when the source is slow or gone, and when the source is gone the record is revoked anyway. Open question 1.

## 5. Threat model

| Threat | What the attacker wants | Defence | What remains |
|---|---|---|---|
| **A lying peer** | Push an unverified file into a friend's library by claiming it is verified, or send different bytes from those it named | The receiver re-verifies everything itself (section 4.2) and ignores the sender's evidence. Bytes are hashed on arrival before anything is stored outside quarantine. Replayed manifests fail because the receiver checks the source live. Basis 3 files are accepted only from peers the admin opted in to, and never passed on | A modified receiving server can accept anything, but that is the attacker's own server |
| **Forged evidence** | A fake archive.org response, a fake licence page, a forged attestation | Evidence is only ever fetched by the server that relies on it, over TLS from a fixed host list (archive.org, commons.wikimedia.org), with no configurable mirror. Raw responses are stored with their hash for review. Attestations are signed by the server key; local sidecar files and peer-supplied evidence are never read | A compromised CA or TLS stack on the host |
| **Hash collisions** | Make a pirated file share hashes with a published one | MD5 chosen-prefix collisions have been practical for years (https://github.com/cr-marcstevens/hashclash) and SHA-1 fell to an identical-prefix collision in 2017 (https://shattered.io/) and a chosen-prefix one in 2020 (https://sha-mbles.github.io/). The attack needs the attacker to **upload** the innocent twin to the trusted host first, then pass it the 30 day age rule. Defences: require MD5 **and** SHA-1 **and** size where the host publishes both; prefer admin-controlled collections over uploader-set licences; VCRX's own identity and peer exchange use SHA-256 only. A cheap extra check worth building: ffprobe the local file and reject it if its container, duration or stream layout differs from what the host reports | Commons publishes SHA-1 only, so a Commons match is SHA-1 plus size. A funded attacker could do it; the reward is sharing one file among friends, which they could do without VCRX |
| **A compromised admin** | Mark anything shareable | No API, CLI or web path sets `verified` directly; only `Authorize`'s verifier does. Editing the database by hand does not get past receivers, who re-verify bases 1 and 2 themselves. For basis 3 the false attestation is signed, named, held by the recipient, capped and one hop. The hash-chained audit log is anchored on friends' servers | Basis 3 is attestation, not proof (section 2.4). A modified binary can bypass the sender's checks, but not an honest receiver's |
| **A malicious band-policy change** | Flip a restricted band or show to permissive, or use a revocation to strand friends' copies | Collection membership on archive.org is admin-controlled, not uploader-controlled. A restrictive to permissive flip is never applied automatically (section 3.4). Free-text `rights` is never parsed, so editing it changes nothing. A genuine revocation is honoured: copies stop being served and are deleted after the grace period | A legitimate policy change can remove a show friends liked. That is correct behaviour |
| **DMCA and legal exposure for the public repo** | A rights holder targets the repo or a maintainer | See below | Not legal advice. Pete should get a lawyer's read before replication ships (open question 6) |
| **Becoming a distribution tool** | Use VCRX to move commercial films between strangers | See section 6 | A fork can remove every check. That is true of any open-source code, and an honest receiver still refuses what it cannot verify |

**DMCA and legal exposure.** The repo holds code, not media, and VCRX instances are run by their owners. The risks are to the code repo and to the people who write it:

- GitHub removes code repositories on DMCA notices even when they hold no copyrighted media: the MPA had the Popcorn Time repos taken down in 2020, and they came back after a counter-notice (https://torrentfreak.com/mpa-suggests-github-could-be-held-liable-for-popcorn-times-copyright-infringements-200505/). The RIAA's 2020 notice against youtube-dl rested on anti-circumvention (section 1201), and GitHub reinstated the project after review (https://github.blog/news-insights/policy-news-and-insights/standing-up-for-developers-youtube-dl-is-back/).
- Liability for others' infringement turns on inducement: in MGM v. Grokster the Supreme Court held that distributing a tool with the object of promoting infringement, shown by clear expression or other steps, creates liability (https://www.law.cornell.edu/supremecourt/text/04-480). Purpose and messaging are evidence.

What the design does about it: VCRX never touches DRM or any copy protection; docs, README and the UI describe only the three bases; tests and examples use only openly licensed fixtures (Big Buck Bunny, CC BY 3.0) and never a commercial title; there is no discovery of strangers, no tracker and no public index; and every refusal is logged, so the project can show what the code actually allows. Each operator is responsible for their own instance, and an operator can import a deny list of SHA-256 hashes that revokes matching records at once.

## 6. Why this does not turn VCRX into a distribution tool

- **Copying is the exception, gated per file by evidence a machine can check,** never a toggle. Everything else is stream-only, as milestone 9 requires, and the route test fails the build otherwise.
- **The sources are already public.** Bases 1 and 2 cover only files archive.org or Commons already offer for free download, and the recommended first version fetches them from that source rather than from a friend.
- **No strangers.** Trading happens only between servers linked by invitation tokens (milestone 9). There is no search across the network for tradeable titles beyond what invited friends list, no tracker and no relay.
- **Receivers verify independently,** so one bad server cannot launder files into honest ones.
- **Home videos go one hop,** with a named, signed attestation and a weekly cap.
- **Off by default,** and nothing is shareable until an admin verifies it file by file.
- **Commercial films are structurally excluded:** they are on neither host under an accepted licence, and any TMDb match bars basis 3.

## 7. Open questions for Pete

1. **First version:** trade references only for bases 1 and 2, with each server downloading from the source (recommended), or peer-to-peer bytes from the start?
2. **Basis 2 hosts:** Wikimedia Commons plus which archive.org collections? A short allowlist is safer; the suggestion is to start with Commons alone.
3. **Basis 3:** build it, in the limited form in section 2.4, or keep home videos stream-only? Recommended: decide after bases 1 and 2 have run.
4. **Basis 3 cap:** is 20 GB per peer per week right?
5. **Revoked copies:** delete from friends' trade stores after how long? Suggested: 7 days, with a notice to that server's admin.
6. **Legal read:** will you get a lawyer's opinion before any replication code merges?
7. **Audio:** basis 1 mostly covers audio, and VCRX has no music library. Is audio in scope, or does basis 1 stay video only and therefore rare?

## 8. Recommended answers (b3 recommendation, Pete to confirm)

The product session's recommendations on the questions above, 2026-10-31. Questions 3 and 6 are Pete's call; the rest stand until he says otherwise.

1. **References only in v1.** Each server downloads from the trusted source itself; no peer-to-peer copying. Peer copying is phase 2 at best, and only after question 6 is settled.
2. **Trusted sources in v1:** archive.org (the Live Music Archive and etree-linked collections) and Wikimedia Commons, nothing else. Adding a host is a code change plus a review, never a config entry.
3. **"My own recording": deferred, needs Pete.** It cannot be proven (section 1.3), which is the reason not to build it now.
4. **No peer cap needed with references only.** Instead, downloads from archive.org are rate limited politely, following its published limits, one download at a time per server, with a queue admins can see.
5. **Revocation:** stop serving at once, and delete local copies within 24 hours, logged in Activity.
6. **A lawyer's opinion is required before any peer-to-peer copying,** so not for references only. Pete's call.
7. **Audio is in scope, and is the main event:** taper-friendly live music is where legal trading lives. It is gated on the music library, so tape trading is sequenced after the music library lands.

The per-file checksum gate that fails closed (section 4) stays exactly as designed.
