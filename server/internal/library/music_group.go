package library

import (
	"path"
	"sort"
	"strings"
)

// VariousArtists is the album artist of a compilation.
const VariousArtists = "Various Artists"

// unknownArtist names an artist when neither the tags nor the folders do.
const unknownArtist = "Unknown Artist"

// musicFile is one audio file as grouping sees it: where it is and what its
// tags say, from a fresh probe or from the row a previous scan stored.
type musicFile struct {
	rel, abs     string
	size, mtime  int64
	tags         map[string]string
	container    string
	codec        string
	durationMs   int64
	bitrate      int64
	sampleRate   int
	channels     int
	bitDepth     int
	embedded     bool
	probeStatus  string
	probeError   string
	probedAt     int64
	needsProbe   bool
	changed      bool
	missingSince int64
}

// musicFolder is what a folder holds besides audio: the show's info file,
// checksum files, and pictures.
type musicFolder struct {
	rel       string
	infoPath  string // relative to the root
	infoText  string
	checksums []folderChecksum
	images    []string // file names in this folder
	hasAudio  bool
}

// folderChecksum is a checksum line with the checksum file it came from,
// both relative to the album folder.
type folderChecksum struct {
	Checksum
	source string
}

// albumPlan is one album as the scan will write it.
type albumPlan struct {
	key         string
	title       string
	artist      *artistPlan
	year        int
	date        string
	genre       string
	compilation bool
	live        bool
	show        Show
	folder      string
	infoPath    string
	infoText    string
	mbid, mbRG  string
	gain, peak  *float64
	coverSource string
	coverPath   string
	checksums   []folderChecksum
	tracks      []*trackPlan
}

type trackPlan struct {
	file        *musicFile
	title       string
	artist      *artistPlan
	artistName  string
	disc, track int
	mbRecording string
	gain, peak  *float64
}

type artistPlan struct {
	key, name, sort string
	mbid            string
	image           string
}

// squashKey makes "MusicBrainz Album Id", "MUSICBRAINZ_ALBUMID" and
// "musicbrainz_albumid" one key, and "album_artist" the same as "albumartist".
func squashKey(k string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '_' || r == '-' {
			return -1
		}
		return r
	}, strings.ToLower(k))
}

type tagSet map[string]string

func squashTags(tags map[string]string) tagSet {
	out := tagSet{}
	for k, v := range tags {
		if sk := squashKey(k); out[sk] == "" {
			out[sk] = v
		}
	}
	return out
}

func (t tagSet) get(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(t[k]); v != "" {
			return v
		}
	}
	return ""
}

func (t tagSet) gain(key string) *float64 {
	if v, ok := gainOf(t[key]); ok {
		return &v
	}
	return nil
}

func (t tagSet) compilation() bool {
	switch strings.ToLower(t.get("compilation", "itunescompilation", "tcmp", "cpil")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// albumFolderOf is the folder an album lives in: the file's own folder, or its
// parent when the file sits in a disc or set folder (CD1, Disc 2, Set 1). disc
// is that folder's number, 0 otherwise.
func albumFolderOf(rel string) (folder string, disc int) {
	dir := parentRel(rel)
	if d := DiscFolder(path.Base(dir)); d > 0 && dir != "" && strings.Contains(dir, "/") {
		return parentRel(dir), d
	}
	return dir, 0
}

// showOf reads a folder as a live show: its name, its info file and whether it
// ships checksums. ok is false for a studio album folder.
func showOf(folder string, f *musicFolder) (Show, bool) {
	byName, named := ParseShowFolder(path.Base(folder))
	var info Show
	if f != nil && f.infoText != "" {
		info = ParseInfoFile(f.infoText)
	}
	sums := f != nil && len(f.checksums) > 0
	hasInfo := f != nil && f.infoPath != ""
	switch {
	case named && (sums || hasInfo || byName.Source != ""):
	case !named && info.Date != "" && sums:
	default:
		return Show{}, false
	}
	sh := byName
	if sh.Date == "" {
		sh.Date = info.Date
	}
	if sh.Venue == "" {
		sh.Venue, sh.Location = info.Venue, info.Location
	} else if sh.Location == "" {
		sh.Location = info.Location
	}
	if sh.Source == "" {
		sh.Source = info.Source
	}
	sh.SourceDetail, sh.Lineage, sh.Taper, sh.Artist = info.SourceDetail, info.Lineage, info.Taper, info.Artist
	return sh, true
}

// mostCommon returns the value most entries have, the first such in order on
// a tie, or "" when there are none.
func mostCommon(vals []string) string {
	counts := map[string]int{}
	best, n := "", 0
	for _, v := range vals {
		if v == "" {
			continue
		}
		counts[v]++
		if counts[v] > n {
			best, n = v, counts[v]
		}
	}
	return best
}

// groupMusic turns a library's audio files into artists, albums and tracks.
// It is a pure function of the files and folders, so every scan regroups the
// whole library from stored tags and nothing depends on scan order.
//
// The album artist comes from the album artist tag; failing that a file
// flagged as a compilation belongs to Various Artists; failing that the
// files of one album title in one folder share their artist, unless they
// name more than one, which makes the album a compilation too. Albums group
// by MusicBrainz release when tagged, else by album artist and title, so the
// two discs of a set in two folders are one album. A live show's folder is
// always its own album: two sources of one show are two recordings.
func groupMusic(files []*musicFile, folders map[string]*musicFolder) ([]*albumPlan, map[string]*artistPlan) {
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	artists := map[string]*artistPlan{}
	artistFor := func(name, mbid string) *artistPlan {
		name = strings.TrimSpace(name)
		if name == "" {
			name = unknownArtist
		}
		key := foldKey(name)
		if key == "" {
			key, name = foldKey(unknownArtist), unknownArtist
		}
		a := artists[key]
		if a == nil {
			a = &artistPlan{key: key, name: name, sort: sortName(name)}
			artists[key] = a
		}
		if a.mbid == "" && mbid != "" {
			a.mbid = mbid
		}
		return a
	}

	type pending struct {
		file        *musicFile
		tags        tagSet
		folder      string
		folderDisc  int
		albumTitle  string
		albumArtist string
		artist      string
	}
	shows := map[string]*Show{}
	isShow := map[string]bool{}
	var all []*pending
	for _, f := range files {
		folder, disc := albumFolderOf(f.rel)
		if _, ok := isShow[folder]; !ok {
			sh, live := showOf(folder, folders[folder])
			isShow[folder] = live
			if live {
				shows[folder] = &sh
			}
		}
		t := squashTags(f.tags)
		p := &pending{file: f, tags: t, folder: folder, folderDisc: disc,
			albumTitle: t.get("album"), albumArtist: t.get("albumartist", "albumartists", "band"), artist: t.get("artist", "artists")}
		if p.albumArtist == "" && t.compilation() {
			p.albumArtist = VariousArtists
		}
		all = append(all, p)
	}

	// Files with no album artist: one title in one folder shares an artist,
	// or is a compilation when its files name several.
	type fk struct{ folder, title string }
	unresolved := map[fk][]*pending{}
	for _, p := range all {
		if p.albumArtist == "" && !isShow[p.folder] {
			k := fk{p.folder, foldKey(p.albumTitle)}
			unresolved[k] = append(unresolved[k], p)
		}
	}
	for _, ps := range unresolved {
		keys := map[string]bool{}
		var names []string
		for _, p := range ps {
			if p.artist != "" {
				keys[foldKey(p.artist)] = true
				names = append(names, p.artist)
			}
		}
		name := mostCommon(names)
		switch {
		case len(keys) > 1:
			name = VariousArtists
		case name == "":
			name = folderArtist(ps[0].folder)
		}
		for _, p := range ps {
			p.albumArtist = name
		}
	}

	albums := map[string]*albumPlan{}
	var order []*albumPlan
	// Live shows: the folder is the album, and its artist is the one most of
	// its files name (a guest on one track does not make it a compilation).
	showArtist := map[string]string{}
	for folder := range shows {
		var aa, a []string
		for _, p := range all {
			if p.folder == folder {
				aa = append(aa, p.tags.get("albumartist", "albumartists", "band"))
				a = append(a, p.artist)
			}
		}
		name := mostCommon(aa)
		if name == "" {
			name = mostCommon(a)
		}
		if name == "" {
			name = shows[folder].Artist
		}
		if name == "" {
			name = folderArtist(folder)
		}
		showArtist[folder] = name
	}

	for _, p := range all {
		var key, title, artistName string
		mbid := p.tags.get("musicbrainzalbumid")
		if sh := shows[p.folder]; sh != nil {
			artistName = showArtist[p.folder]
			key = "live:" + p.folder
			title = p.albumTitle
			if title == "" {
				title = strings.TrimSpace(sh.Date + " " + sh.Venue)
				if sh.Location != "" && sh.Venue != "" {
					title += ", " + sh.Location
				}
			}
		} else {
			artistName = p.albumArtist
			title = p.albumTitle
			if title == "" {
				title = folderTitle(p.folder)
			}
			key = "album:" + foldKey(artistName) + "|" + foldKey(title)
			if mbid != "" {
				key = "mb:" + strings.ToLower(mbid)
			}
		}
		al := albums[key]
		if al == nil {
			al = &albumPlan{key: key, title: title, folder: p.folder, live: shows[p.folder] != nil,
				artist: artistFor(artistName, p.tags.get("musicbrainzalbumartistid"))}
			if sh := shows[p.folder]; sh != nil {
				al.show = *sh
				al.date = sh.Date
				al.year = yearOf(sh.Date)
			}
			if f := folders[p.folder]; f != nil {
				al.infoPath, al.infoText, al.checksums = f.infoPath, f.infoText, f.checksums
			}
			albums[key] = al
			order = append(order, al)
		}

		_, fileTrack, nameTitle := TrackNumbers(p.file.rel)
		fileDisc, _, _ := TrackNumbers(p.file.rel)
		tp := &trackPlan{file: p.file, title: p.tags.get("title"),
			disc: numberOf(p.tags.get("disc", "discnumber", "disk")), track: numberOf(p.tags.get("track", "tracknumber")),
			mbRecording: p.tags.get("musicbrainztrackid", "musicbrainzrecordingid"),
			gain:        p.tags.gain("replaygaintrackgain"), peak: p.tags.gain("replaygaintrackpeak")}
		if tp.title == "" {
			tp.title = nameTitle
		}
		if tp.disc == 0 {
			tp.disc = p.folderDisc
		}
		if tp.disc == 0 {
			tp.disc = fileDisc
		}
		if tp.track == 0 {
			tp.track = fileTrack
		}
		tp.artistName = p.artist
		if tp.artistName == "" {
			tp.artistName = al.artist.name
		}
		tp.artist = artistFor(tp.artistName, p.tags.get("musicbrainzartistid"))
		if tp.artist == al.artist && al.artist.mbid == "" {
			al.artist.mbid = p.tags.get("musicbrainzartistid")
		}
		al.tracks = append(al.tracks, tp)
		if p.albumArtist == VariousArtists || p.tags.compilation() {
			al.compilation = true
		}
	}

	for _, al := range order {
		sort.SliceStable(al.tracks, func(i, j int) bool {
			a, b := al.tracks[i], al.tracks[j]
			if a.disc != b.disc {
				return a.disc < b.disc
			}
			if a.track != b.track {
				return a.track < b.track
			}
			return a.file.rel < b.file.rel
		})
		var genres, dates []string
		for _, tp := range al.tracks {
			t := squashTags(tp.file.tags)
			genres = append(genres, t.get("genre"))
			if d := dateOf(t.get("date", "originaldate", "year")); d != "" {
				dates = append(dates, d)
			}
			if al.mbid == "" {
				al.mbid = t.get("musicbrainzalbumid")
			}
			if al.mbRG == "" {
				al.mbRG = t.get("musicbrainzreleasegroupid")
			}
			if al.gain == nil {
				al.gain, al.peak = t.gain("replaygainalbumgain"), t.gain("replaygainalbumpeak")
			}
			if al.coverSource == "" && tp.file.embedded && tp.file.missingSince == 0 {
				al.coverSource, al.coverPath = "embedded", tp.file.rel
			}
		}
		al.genre = mostCommon(genres)
		if al.date == "" && len(dates) > 0 {
			sort.Strings(dates)
			al.date = dates[0]
		}
		if al.year == 0 {
			al.year = yearOf(al.date)
		}
		// A picture beside the tracks beats one inside them.
		if f := folders[al.folder]; f != nil {
			if img := coverImage(f.images); img != "" {
				al.coverSource, al.coverPath = "file", joinRel(al.folder, img)
			}
		}
	}
	// An artist's own folder (the parent of an album folder) may hold their
	// picture.
	for _, al := range order {
		if al.artist.image != "" || al.folder == "" {
			continue
		}
		parent := parentRel(al.folder)
		if f := folders[parent]; f != nil && parent != "" && !f.hasAudio {
			if img := artistImage(f.images); img != "" {
				al.artist.image = joinRel(parent, img)
			}
		}
	}
	return order, artists
}

// folderArtist guesses the artist from an Artist/Album layout: the album
// folder's parent, when there is one under the root.
func folderArtist(folder string) string {
	parent := parentRel(folder)
	if parent == "" {
		return unknownArtist
	}
	return path.Base(parent)
}

// folderTitle is an album title from its folder name, or "Unknown Album" for
// files loose in the root.
func folderTitle(folder string) string {
	if folder == "" {
		return "Unknown Album"
	}
	return path.Base(folder)
}

var coverNames = []string{"cover", "folder", "front", "album", "albumart", "albumartlarge"}

// coverImage picks an album's picture from a folder's image files.
func coverImage(names []string) string {
	return pickImage(names, coverNames)
}

func artistImage(names []string) string {
	return pickImage(names, []string{"artist", "folder", "band"})
}

func pickImage(names, stems []string) string {
	for _, stem := range stems {
		for _, n := range names {
			if strings.EqualFold(stripExt(n), stem) {
				return n
			}
		}
	}
	return ""
}

// isImage reports whether a file is a picture a folder might hold.
func isImage(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}
