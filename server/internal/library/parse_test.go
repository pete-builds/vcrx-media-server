package library

import (
	"strings"
	"testing"
)

// Every fixture below reproduces a naming shape seen in a real library, with
// invented or public-domain titles and invented release groups.

type movieCase struct {
	in      string
	title   string
	year    int
	edition string
	res     string
	source  string
	codec   string
	hdr     string // comma-joined
	threeD  bool
}

var movieNameCases = []movieCase{
	// Scene dot names.
	{in: "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-VCRXTEST", title: "Night of the Living Dead", year: 1968, res: "1080p", source: "BluRay", codec: "H.264"},
	{in: "Nosferatu.1922.720p.BluRay.x264-SAMPLEGRP", title: "Nosferatu", year: 1922, res: "720p", source: "BluRay", codec: "H.264"},
	{in: "His.Girl.Friday.1940.REMASTERED.1080p.BluRay.x265-VCRXTEST", title: "His Girl Friday", year: 1940, edition: "Remastered", res: "1080p", source: "BluRay", codec: "HEVC"},
	{in: "Plan.9.from.Outer.Space.1959.DVDRip.XviD-SAMPLEGRP", title: "Plan 9 from Outer Space", year: 1959, source: "DVDRip", codec: "XviD"},
	{in: "Quorvex.1963.2160p.UHD.BluRay.REMUX.HDR.HEVC.DTS-HD.MA.5.1-VCRXTEST", title: "Quorvex", year: 1963, res: "4K", source: "REMUX", codec: "HEVC", hdr: "HDR"},
	{in: "Velloran.1927.2160p.WEB-DL.DV.HDR10.H.265-SAMPLEGRP", title: "Velloran", year: 1927, res: "4K", source: "WEB-DL", codec: "HEVC", hdr: "DV,HDR"},
	{in: "Tarrowby.1945.1080p.AMZN.WEB-DL.DDP2.0.H.264-VCRXTEST", title: "Tarrowby", year: 1945, res: "1080p", source: "WEB-DL", codec: "H.264"},
	{in: "Q.R.X.1950.720p.WEBRip.AAC2.0.x264-SAMPLEGRP", title: "Q.R.X.", year: 1950, res: "720p", source: "WEBRip", codec: "H.264"},
	{in: "The.Pellingham.Gear.Shop.1960.PROPER.1080p.BluRay.x264-VCRXTEST", title: "The Pellingham Gear Shop", year: 1960, res: "1080p", source: "BluRay", codec: "H.264"},
	{in: "Brindlemarsh.Carnival.1962.Criterion.Collection.1080p.BluRay.x264-SAMPLEGRP", title: "Brindlemarsh Carnival", year: 1962, edition: "Criterion", res: "1080p", source: "BluRay"},
	{in: "The.Saltwick.Kid.1921.EXTENDED.720p.BluRay.x264-VCRXTEST", title: "The Saltwick Kid", year: 1921, edition: "Extended", res: "720p", source: "BluRay"},
	{in: "Ostrander.Jr.1924.1080p.BluRay.x264-SAMPLEGRP", title: "Ostrander Jr", year: 1924, res: "1080p", source: "BluRay"},
	{in: "Quillon.Last!.1923.1080p.BluRay.x264-VCRXTEST", title: "Quillon Last!", year: 1923, res: "1080p"},
	{in: "Ledger.of.Dr.Quell.1920.RESTORED.720p.BluRay.x264-SAMPLEGRP", title: "Ledger of Dr Quell", year: 1920, res: "720p"},
	{in: "A.Trip.to.Velloran.1902.720p.BluRay.x264-VCRXTEST", title: "A Trip to Velloran", year: 1902, res: "720p"},
	{in: "The.Lantern.of.Marrowby.1925.Directors.Cut.2160p.WEB-DL.x265-SAMPLEGRP", title: "The Lantern of Marrowby", year: 1925, edition: "Director's Cut", res: "4K"},
	{in: "House.on.Haunted.Hill.1959.Colorized.1080p.WEB-DL.H264-VCRXTEST", title: "House on Haunted Hill", year: 1959, res: "1080p", source: "WEB-DL", codec: "H.264"},
	{in: "Tenant.of.Pellingham.1964.UNRATED.1080p.BluRay.x264-SAMPLEGRP", title: "Tenant of Pellingham", year: 1964, edition: "Unrated", res: "1080p"},
	{in: "Quillspire.Madness.1936.HDTV.x264-VCRXTEST", title: "Quillspire Madness", year: 1936, source: "HDTV", codec: "H.264"},
	{in: "Brassmoor.Wedding.1951.IMAX.Edition.1080p.BluRay.x264-SAMPLEGRP", title: "Brassmoor Wedding", year: 1951, edition: "IMAX", res: "1080p"},
	{in: "Harrowgate!.1963.Collectors.Edition.720p.BluRay.x264-VCRXTEST", title: "Harrowgate!", year: 1963, edition: "Collector's Cut", res: "720p"},
	{in: "Saltwick.Serenade.1941.Theatrical.Cut.2160p.UHD.BluRay.x265-SAMPLEGRP", title: "Saltwick Serenade", year: 1941, edition: "Theatrical", res: "4K"},
	{in: "Meet.Orvel.Quine.1941.1080p.BluRay.3D.HSBS.x264-VCRXTEST", title: "Meet Orvel Quine", year: 1941, res: "1080p", threeD: true},
	{in: "The.Velloran.Stranger.1946.MULTi.1080p.BluRay.x264-SAMPLEGRP", title: "The Velloran Stranger", year: 1946, res: "1080p"},
	{in: "Cobalt.Quayside.1945.LIMITED.720p.BluRay.x264-VCRXTEST", title: "Cobalt Quayside", year: 1945, res: "720p"},
	{in: "Tarrowby.Harbor.Files.1952.REPACK.1080p.BluRay.x264-SAMPLEGRP", title: "Tarrowby Harbor Files", year: 1952},
	{in: "The.Rope-Walker.1953.1080p.BluRay.x264-VCRXTEST", title: "The Rope-Walker", year: 1953, res: "1080p"},
	{in: "Quorvex.13.1963.1080p.BluRay.x264-SAMPLEGRP", title: "Quorvex 13", year: 1963},
	{in: "Pellingham.Outwits.the.Moonfolk.1964.720p.HDTV.x264-VCRXTEST", title: "Pellingham Outwits the Moonfolk", year: 1964, source: "HDTV"},
	{in: "The.Clock.That.Wouldnt.Stop.1962.DVDScr.XviD.AC3-SAMPLEGRP", title: "The Clock That Wouldnt Stop", year: 1962, source: "DVDScr", codec: "XviD"},
	{in: "Orvels.Wanderings.1939.HDRip.XviD.AC3-VCRXTEST", title: "Orvels Wanderings", year: 1939, source: "HDRip"},
	{in: "The.General.1926.1080p.BluRay.HEVC.10bit.DTS-SAMPLEGRP", title: "The General", year: 1926, codec: "HEVC"},
	{in: "L.A.Velloran.1955.720p.BluRay.x264-VCRXTEST", title: "L.A. Velloran", year: 1955},
	{in: "U.S.Wardens.of.Quorvex.2011.720p.BluRay.x264-SAMPLEGRP", title: "U.S. Wardens of Quorvex", year: 2011},
	{in: "Teenagers.Beyond.Planet.Quorvex.1959.READNFO.720p.BluRay.x264-VCRXTEST", title: "Teenagers Beyond Planet Quorvex", year: 1959},
	{in: "Abruptly.1954.HC.HDRip.XViD-SAMPLEGRP", title: "Abruptly", year: 1954, source: "HDRip"},
	// Space separated, with DD5 1 and H 264 split by spaces.
	{in: "Night of the Living Dead 1968 1080p WEB-DL DD5 1 H 264-VCRXTEST", title: "Night of the Living Dead", year: 1968, res: "1080p", source: "WEB-DL", codec: "H.264"},
	{in: "His Girl Friday 1940 720p BluRay x264-SAMPLEGRP", title: "His Girl Friday", year: 1940, res: "720p"},
	{in: "Plan 9 from Outer Space 1959 DVDSCR XviD AC3-VCRXTEST ~FX~", title: "Plan 9 from Outer Space", year: 1959, source: "DVDScr"},
	{in: "Tarrowby 1945 720p WEBRip AAC 2 0 X264-SAMPLEGRP", title: "Tarrowby", year: 1945, source: "WEBRip"},
	{in: "Quorvex 1963 2160p WEB-DL X265 HDR DD 5 1 Atmos VCRXTEST", title: "Quorvex", year: 1963, res: "4K", hdr: "HDR"},
	{in: "Velloran 1927 1080p Bluray Atmos TrueHD 7 1 X264-SAMPLEGRP", title: "Velloran", year: 1927, res: "1080p", source: "BluRay"},
	{in: "The Saltwick Kid 1921 MULTi 1080p WEBRip X264-VCRXTEST -FX-", title: "The Saltwick Kid", year: 1921},
	{in: "Nosferatu 1922 BR 720p X264 VCRXQ", title: "Nosferatu", year: 1922, res: "720p", source: "BluRay"},
	{in: "Brindlemarsh Carnival 1962 BRRip X264 720p-VCRXTEST", title: "Brindlemarsh Carnival", year: 1962, res: "720p", source: "BluRay"},
	{in: "Quillspire Madness 1936 KORSUB 720p HDRip x264 AC3-SAMPLEGRP", title: "Quillspire Madness", year: 1936},
	{in: "Tarrowby 1945 Theatrical Cut 576p DVDRip x264-VCRXTEST ~FX~", title: "Tarrowby", year: 1945, edition: "Theatrical", res: "576p"},
	{in: "The Velloran Stranger 1946 1080p BluRay DTS HDMA 5 1 X264 NLSubs VCRXTEST", title: "The Velloran Stranger", year: 1946},
	{in: "Cobalt Quayside 1945 DVDScr XVID AC3 HQ SAMPLEGRP", title: "Cobalt Quayside", year: 1945, source: "DVDScr"},
	{in: "Brassmoor Wedding 1951 Limited 720p Bluray X264-vcrxtest", title: "Brassmoor Wedding", year: 1951},
	// Year folders.
	{in: "Night of the Living Dead (1968)", title: "Night of the Living Dead", year: 1968},
	{in: "His Girl Friday (1940) 1080p Bluray AC3 X264 - VCRXTEST", title: "His Girl Friday", year: 1940, res: "1080p"},
	{in: "Nosferatu (1922).720p.AC3-SAMPLEGRP", title: "Nosferatu", year: 1922, res: "720p"},
	{in: "The Saltwick Kid (1921).720p.Blu-Ray .AAC-SAMPLEGRP", title: "The Saltwick Kid", year: 1921, source: "BluRay"},
	{in: "Saltwick Serenade (1941) 720p MKV x264 DTS BDRip SAMPLEGRP", title: "Saltwick Serenade", year: 1941, source: "BluRay"},
	{in: "Good.Evening.Quorvex.(1997).BluRay.1080p.x264.DTS-VCRXTEST", title: "Good Evening Quorvex", year: 1997, res: "1080p"},
	{in: "Meet Orvel Quine (1941) 1080p By VCRXTEST", title: "Meet Orvel Quine", year: 1941},
	{in: "Quorvex (1963) [1080p] [BluRay] [5.1]", title: "Quorvex", year: 1963, res: "1080p", source: "BluRay"},
	// Years and numbers that are part of the title.
	{in: "1931.2019.2160p.UHD.BluRay.x265-VCRXTEST", title: "1931", year: 2019, res: "4K"},
	{in: "1931 (2019)", title: "1931", year: 2019},
	{in: "2004 A Velloran Odyssey 1968 1080p BluRay DTS x264-SAMPLEGRP", title: "2004 A Velloran Odyssey", year: 1968},
	{in: "2004.A.Velloran.Odyssey.(1968)", title: "2004 A Velloran Odyssey", year: 1968},
	{in: "1999.1999.720p.BluRay.x264-VCRXTEST", title: "1999", year: 1999},
	{in: "1999 1999 720p BluRay x264-SAMPLEGRP", title: "1999", year: 1999},
	{in: "12.Harbor.Lane.2016.720p.WEBRip.AAC.2.0.x264-VCRXTEST", title: "12 Harbor Lane", year: 2016},
	{in: "12.Harbor.Lane.(2016).HEVC.1080p.WEB.AAC.-SAMPLEGRP-", title: "12 Harbor Lane", year: 2016, res: "1080p"},
	{in: "Station 2049 2017 2160p UHD BluRay REMUX HDR HEVC Atmos-VCRXTEST", title: "Station 2049", year: 2017, res: "4K", source: "REMUX", hdr: "HDR"},
	{in: "Station.2049.1080p.BluRay.x264-SAMPLEGRP", title: "Station 2049", year: 0, res: "1080p"},
	{in: "Velloran 1985 (2020)", title: "Velloran 1985", year: 2020},
	{in: "Graduates of 1983 (1981)", title: "Graduates of 1983", year: 1981},
	{in: "2024 2009 1080p BluRay x264-VCRXTEST", title: "2024", year: 2009},
	{in: "1955 2017 MULTi 1080p WEBRip X264-SAMPLEGRP", title: "1955", year: 2017},
	{in: "3 33 2017 1080p BluRay X264-VCRXTEST", title: "3 33", year: 2017},
	{in: "1931.1080p.BluRay.x264-VCRXTEST", title: "1931", year: 0, res: "1080p"},
	// Titles with leading numbers.
	{in: "14 Silent Clockmakers (1958)", title: "14 Silent Clockmakers", year: 1958},
	{in: "4 Weeks in Saltwick 2014 720p BluRay x264-VCRXTEST", title: "4 Weeks in Saltwick", year: 2014},
	{in: "11.Rules.for.Quorvex.1999.720p.BluRay.x264-SAMPLEGRP", title: "11 Rules for Quorvex", year: 1999},
	{in: "60_Second_Quills", title: "60 Second Quills"},
	{in: "3q-velloran.1999.1080p", title: "3q-velloran", year: 1999, res: "1080p"},
	{in: "400 2006 MULTISUBS 1080p BluRay DTS X264-VCRXTEST", title: "400", year: 2006},
	// All caps and brackets.
	{in: "77 SPOTTED QUILLS[DVDRIP][ENG]-samplegrp", title: "77 Spotted Quills", source: "DVDRip"},
	{in: "NIGHT OF THE LIVING DEAD (1968) X264 1080p WEB-DL DD5 1 NLSubs -VCRXTEST", title: "Night of the Living Dead", year: 1968, res: "1080p"},
	{in: "NOSFERATU.1922.1080p.(WEB-DL).x264.AAC-SAMPLEGRP", title: "Nosferatu", year: 1922, res: "1080p", source: "WEB-DL"},
	{in: "TARROWBY (1945) DVDScreener AAC -VCRXTEST", title: "Tarrowby", year: 1945, source: "DVDScr"},
	{in: "QRV 2016 MULTi 1080p WEBRip X264-SAMPLEGRP", title: "QRV", year: 2016},
	{in: "VCRX.2023.MULTI.1080p.WEB.H264-SAMPLEGRP", title: "VCRX", year: 2023, source: "WEB"},
	{in: "[VCRXTEST] Quorvex (1963) [1080p]", title: "Quorvex", year: 1963, res: "1080p"},
	{in: "Quorvex 1963 [BluRay] [720p] [VCRXTEST]", title: "Quorvex", year: 1963, res: "720p", source: "BluRay"},
	// Underscores and mixed separators.
	{in: "Return_To_Quorvex_Isle_2013_BluRay_720p_H264-7-11", title: "Return To Quorvex Isle", year: 2013, res: "720p"},
	{in: "Beyond_Velloran_2013_720P_BRRIP_H264_AAC_SAMPLEGRP", title: "Beyond Velloran", year: 2013, res: "720p", source: "BluRay"},
	{in: "A_Jolly_Quorvex_Winter_2014_WEB-DLX_AAC_VCRXTEST", title: "A Jolly Quorvex Winter", year: 2014, source: "WEB-DL"},
	{in: "Saltwick_Quill_Club_2013_DVDScr_XVID_AC3_SAMPLEGRP_(FX)", title: "Saltwick Quill Club", year: 2013, source: "DVDScr"},
	{in: "Foggy_Days_Over_Quorvex_2_2013_720p_PPVRip_x264_AAC-VCRXTEST_[FX2]", title: "Foggy Days Over Quorvex 2", year: 2013},
	{in: "Quorvex_Bargain_(2013)_BluRay_720p", title: "Quorvex Bargain", year: 2013, res: "720p"},
	// No year at all.
	{in: "Quorvex Really 720p.x264.BRRip.SAMPLEGRP", title: "Quorvex Really", res: "720p", source: "BluRay"},
	{in: "Quillborne 720p BRRip x264 AAC (FX Release)", title: "Quillborne", res: "720p"},
	{in: "Quorvex.Protocol.1080p.BluRay.x264-VCRXTEST", title: "Quorvex Protocol", res: "1080p"},
	{in: "Velloran_Racketeer", title: "Velloran Racketeer"},
	{in: "Quillbeast-1080p WEB AAC 2009-FX", title: "Quillbeast", year: 2009, res: "1080p"},
	{in: "Quorvex Nectar.2016.720.X264-SAMPLEGRP", title: "Quorvex Nectar", year: 2016, res: "720p"},
	{in: "Orvel's VCRX 2006 720p BluRay x264-SAMPLEGRP", title: "Orvel's VCRX", year: 2006},
	{in: "Quorvex Rising 3D (2010)", title: "Quorvex Rising 3D", year: 2010},
	{in: "Directive: Quorvex (1996)", title: "Directive: Quorvex", year: 1996},
	// Editions before the year.
	{in: "Quorvex.Cousins.Unrated.2008.BluRay.1080p.DTS.x264-VCRXTEST", title: "Quorvex Cousins", year: 2008, edition: "Unrated", res: "1080p"},
	{in: "A.Very.Quorvex.Winter.2012.EXTENDED.720p.BluRay.x264-SAMPLEGRP", title: "A Very Quorvex Winter", year: 2012, edition: "Extended"},
	{in: "Velloran.1997.EXTENDED.MULTi.1080p.BluRay.x264-VCRXTEST", title: "Velloran", year: 1997, edition: "Extended"},
	{in: "Quorvex Tart 1998 UNRATED DTS-HD NORDICSUBS 1080p BluRay x264-SAMPLEGRP", title: "Quorvex Tart", year: 1998, edition: "Unrated"},
	// Leading junk.
	{in: "~~ -- . Starflight.2012.1080p.BluRay.X264-VCRXTEST", title: "Starflight", year: 2012},
	{in: "=FXTEAM=BD50.Quorvex.Harbor.2006.1080p.DTS", title: "Quorvex Harbor", year: 2006},
	{in: "1080p.WEB-DL.x265-VCRXTEST", title: "", res: "1080p"},
	{in: "4k Demo Reel 3", title: ""},
	{in: "Quorvex Waits! (2026)", title: "Quorvex Waits!", year: 2026},
	{in: "Mr. Quillby (2011)", title: "Mr. Quillby", year: 2011},
	{in: "Quorvex.Engine.2015.720p.BluRay.x264-VCRXTEST", title: "Quorvex Engine", year: 2015},
	{in: "Velloran (2015) 720p HDRip HC x264 AAC-VCRXTEST", title: "Velloran", year: 2015, source: "HDRip"},
	{in: "Quorvex Gate 2014 720p HDTS x264 AC3-VCRXTEST", title: "Quorvex Gate", year: 2014, source: "TS"},
	{in: "Velloran.Wake.2005.BDRip.H264.AAC-VCRXTEST (FX-Release)", title: "Velloran Wake", year: 2005, source: "BluRay"},
	// Download client and Usenet wrappers.
	{in: "_UNPACK_Tarrowby.1945.1080p.BluRay.x264-VCRXTEST", title: "Tarrowby", year: 1945, res: "1080p"},
	{in: "_FAILED_Tarrowby.1945.720p.WEB-DL", title: "Tarrowby", year: 1945, res: "720p"},
	{in: "Classics Pack Vol 2 [04+30] - His.Girl.Friday.1940.720p", title: "His Girl Friday", year: 1940, res: "720p"},
	{in: "[1+9] - Nosferatu.1922.1080p.BluRay", title: "Nosferatu", year: 1922},
	// Plex and Jellyfin edition syntax.
	{in: "Quorvex Manor (1932) {edition-Director's Cut}", title: "Quorvex Manor", year: 1932, edition: "Director's Cut"},
	{in: "Quorvex Manor (1932) {edition-Workprint} 1080p", title: "Quorvex Manor", year: 1932, edition: "Workprint", res: "1080p"},
	// Title punctuation survives.
	{in: "Salt & Quills (2025)", title: "Salt & Quills", year: 2025},
	{in: "Orvel, Quine & Saltwick (2026)", title: "Orvel, Quine & Saltwick", year: 2026},
	{in: "Velloran - Tidewater – Brass Lantern (2026)", title: "Velloran - Tidewater – Brass Lantern", year: 2026},
	{in: "THE.QUORVEX.AFFAIR.(2015).1080p.Bluray.DTS.NL.Subs English", title: "The Quorvex Affair", year: 2015, res: "1080p"},
	// Language and region tags.
	{in: "Velloran.2025.1080p.H.264.iTA.AC3-SAMPLEGRP", title: "Velloran", year: 2025, codec: "H.264"},
	{in: "Quorvex.Man.2024.SWESUB.1080p.WEB-DL.H.264-VCRXTEST", title: "Quorvex Man", year: 2024, source: "WEB-DL"},
	{in: "Velloran.Hunt.2026.FRE-ENG.1080p.WEB-DL.x264-VCRXTEST", title: "Velloran Hunt", year: 2026, res: "1080p"},
	{in: "Quorvex.Calls.2010.GERMAN.DL.1080p.WEB.H264-VCRXTEST", title: "Quorvex Calls", year: 2010, source: "WEB"},
	{in: "The.Quillery.2026.1080p.WEB-DL.DDP5.1.H.264-VCRXTEST", title: "The Quillery", year: 2026, codec: "H.264"},
	{in: "Quorvex_Is_Waiting_2026_1080p_WEB-DL_H_264_DDP5_1-VCRXTEST", title: "Quorvex Is Waiting", year: 2026, codec: "H.264"},
	{in: "Quorvex Down (2009) 1080p Dutch & English", title: "Quorvex Down", year: 2009},
	{in: "Quill-Runner.Tidewater.2021.IMAX.1080p.WEB-DL.DDP5.1.Atmos.H.264-VCRXTEST", title: "Quill-Runner Tidewater", year: 2021, edition: "IMAX"},
	{in: "Quorvex.Saga_5.2026.1080p.WEB-DL.MULTI.H.264-VCRXTEST", title: "Quorvex Saga 5", year: 2026},
	{in: "Quorvex Hounds 2016 1080p BluRay 6CH SAMPLEGRP", title: "Quorvex Hounds", year: 2016},
	{in: "Quorvex.Dawn.2026.Hybrid.2160p.WEB-DL.DV.HDR.H265-VCRXTEST", title: "Quorvex Dawn", year: 2026, res: "4K", hdr: "DV,HDR"},
	{in: "Quorvexing.2025.1800p.WEB-DL.H.264-VCRXTEST", title: "Quorvexing", year: 2025, source: "WEB-DL"},
}

func TestParseMovieName(t *testing.T) {
	for _, c := range movieNameCases {
		t.Run(c.in, func(t *testing.T) {
			m := ParseMovieName(c.in)
			checkMovie(t, m, c)
		})
	}
}

func checkMovie(t *testing.T, m Movie, c movieCase) {
	t.Helper()
	if m.Title != c.title {
		t.Errorf("title = %q, want %q", m.Title, c.title)
	}
	if m.Year != c.year {
		t.Errorf("year = %d, want %d", m.Year, c.year)
	}
	if m.Edition != c.edition {
		t.Errorf("edition = %q, want %q", m.Edition, c.edition)
	}
	if c.res != "" && m.Resolution != c.res {
		t.Errorf("resolution = %q, want %q", m.Resolution, c.res)
	}
	if c.source != "" && m.Source != c.source {
		t.Errorf("source = %q, want %q", m.Source, c.source)
	}
	if c.codec != "" && m.VideoCodec != c.codec {
		t.Errorf("codec = %q, want %q", m.VideoCodec, c.codec)
	}
	if c.hdr != "" && strings.Join(m.HDR, ",") != c.hdr {
		t.Errorf("hdr = %v, want %q", m.HDR, c.hdr)
	}
	if m.ThreeD != c.threeD {
		t.Errorf("3D = %v, want %v", m.ThreeD, c.threeD)
	}
}

type moviePathCase struct {
	rel    string
	oneTtl bool
	title  string
	year   int
	res    string
}

var moviePathCases = []moviePathCase{
	// The folder beats a lowercase abbreviated or hashed file name.
	{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-VCRXTEST/vcrxtest-notld-1080p.mkv", true, "Night of the Living Dead", 1968, "1080p"},
	{"Nosferatu 1922 720p BluRay X264-SAMPLEGRP/x-nosferatu.720p.mkv", true, "Nosferatu", 1922, "720p"},
	{"His Girl Friday 1940 1080p WEB-DL DD5 1 H264-VCRXTEST/0f3c9a7e5b1d4c2a8e6f9b0d7c5a3e1f.mkv", true, "His Girl Friday", 1940, "1080p"},
	{"Tarrowby 1945 720p BluRay X264-VCRXTEST -FX2-/kQ7xT2mZp9LwR4vNcY8bHs3Ud6Ge1Jf.mkv", true, "Tarrowby", 1945, "720p"},
	{"Quorvex (1963)/Quorvex.1963.1080p.AMZN.WEB-DL.DDP5.1.H.264-SAMPLEGRP.mkv", true, "Quorvex", 1963, "1080p"},
	{"Velloran.1927.1080p.WEB-DL.X264.AC3-VCRXTEST/Velloran.1927.1080p.WEB-DL.X264.AC3-VCRXTEST/Velloran.1927.1080p.WEB-DL.X264.AC3-VCRXTEST.mkv", true, "Velloran", 1927, "1080p"},
	// The folder has no year but the file does.
	{"Velloran_Racketeer/Velloran.Racketeer.2007.UNRATED.720p.BRRip.Xvid.AC3.mkv", true, "Velloran Racketeer", 2007, "720p"},
	// A collection folder names none of its films.
	{"Public Domain Classics Collection/Plan.9.from.Outer.Space.1959.720p.BluRay.x264.mp4", false, "Plan 9 from Outer Space", 1959, "720p"},
	{"Public Domain Classics Collection/His.Girl.Friday.1940.1080p.BluRay.x264.mp4", false, "His Girl Friday", 1940, "1080p"},
	// Generic folders are skipped on the way up.
	{"Quillspire Madness (1936)/CD1/quillspire.madness.cd1.avi", true, "Quillspire Madness", 1936, ""},
	{"4K/The.General.1926.2160p.UHD.BluRay.x265-VCRXTEST.mkv", true, "The General", 1926, "4K"},
	{"Brassmoor Wedding (1951)/Brassmoor Wedding (1951) - 2160p.mkv", true, "Brassmoor Wedding", 1951, "4K"},
	// A file at the root has only its own name.
	{"The.Saltwick.Kid.1921.1080p.BluRay.x264-VCRXTEST.mkv", true, "The Saltwick Kid", 1921, "1080p"},
	// A lowercase scene abbreviation in the folder loses to a cased file name.
	{"vcrxtest-q.v.v.1999.1080p/Quorvex.Voyage.Volume.Five.1999.DTS-HD.mkv", true, "Quorvex Voyage Volume Five", 1999, "1080p"},
	{"Quorvex Manor (1932)/Quorvex Manor (1932) {edition-Director's Cut}.mkv", true, "Quorvex Manor", 1932, ""},
	{"_UNPACK_Tarrowby.1945.1080p.BluRay.x264-VCRXTEST/Tarrowby.1945.1080p.BluRay.x264-VCRXTEST.mkv", true, "Tarrowby", 1945, "1080p"},
	{"Tarrowby (1945) [1080p]/Tarrowby.1945.1080p.BrRip.x264.mp4", true, "Tarrowby", 1945, "1080p"},
	{"Quorvex Tide ()/Quorvex.Tide.2026.1080p.WEB.H264-VCRXTEST.mkv", true, "Quorvex Tide", 2026, "1080p"},
}

func TestParseMoviePath(t *testing.T) {
	for _, c := range moviePathCases {
		t.Run(c.rel, func(t *testing.T) {
			m := ParseMoviePath(c.rel, c.oneTtl)
			if m.Title != c.title || m.Year != c.year {
				t.Errorf("got %q (%d), want %q (%d)", m.Title, m.Year, c.title, c.year)
			}
			if c.res != "" && m.Resolution != c.res {
				t.Errorf("resolution = %q, want %q", m.Resolution, c.res)
			}
		})
	}
}

type episodeCase struct {
	rel      string
	strict   bool
	ok       bool
	show     string
	showYear int
	season   int
	episode  int
	end      int
	title    string
	date     string
	showDir  string
}

var episodeCases = []episodeCase{
	// Show/Season N/file trees.
	{rel: "The Quorvex Mysteries/Season 1/The.Quorvex.Mysteries.-.S01E02.-.The.Brass.Kettle.WEBDL-1080p.mkv", ok: true, show: "The Quorvex Mysteries", season: 1, episode: 2, title: "The Brass Kettle", showDir: "The Quorvex Mysteries"},
	{rel: "Saltwick Patrol/Season 8/Saltwick.Patrol.-.S08E23.-.Harbor.Lights.Go.Dark.(1).Bluray-1080p.mp4", ok: true, show: "Saltwick Patrol", season: 8, episode: 23, title: "Harbor Lights Go Dark (1)", showDir: "Saltwick Patrol"},
	{rel: "Saltwick Patrol/Season 6/Saltwick.Patrol.-.S06E20.-.Kettles.and.Q.U.I.L.L.Bluray-1080p.mkv", ok: true, show: "Saltwick Patrol", season: 6, episode: 20, title: "Kettles and Q.U.I.L.L.", showDir: "Saltwick Patrol"},
	{rel: "Saltwick Patrol/Season 10/Saltwick.Patrol.-.S10E17-18.-.Tide.Turns.Twice.WEBDL-1080p.mkv", ok: true, show: "Saltwick Patrol", season: 10, episode: 17, end: 18, title: "Tide Turns Twice", showDir: "Saltwick Patrol"},
	{rel: "Velloran Nights (2020)/Season 2/Velloran Nights (2020) - S02E05 - Quill Day.mkv", ok: true, show: "Velloran Nights", showYear: 2020, season: 2, episode: 5, title: "Quill Day", showDir: "Velloran Nights (2020)"},
	{rel: "Brindlemarsh/Season 01/Brindlemarsh.S01E01E02.Opening.Bell.720p.HDTV.x264-VCRXTEST.mkv", ok: true, show: "Brindlemarsh", season: 1, episode: 1, end: 2, title: "Opening Bell", showDir: "Brindlemarsh"},
	{rel: "Brindlemarsh/Season 2/Brindlemarsh.S02E03-E04.Two.Parter.1080p.WEB.h264-VCRXTEST.mkv", ok: true, show: "Brindlemarsh", season: 2, episode: 3, end: 4, title: "Two Parter", showDir: "Brindlemarsh"},
	{rel: "Brindlemarsh/Season 3/Brindlemarsh - s03e07 - Whiteout.mkv", ok: true, show: "Brindlemarsh", season: 3, episode: 7, title: "Whiteout", showDir: "Brindlemarsh"},
	{rel: "Brindlemarsh/Season 4/04 - Thaw.mkv", ok: true, show: "Brindlemarsh", season: 4, episode: 4, title: "Thaw", showDir: "Brindlemarsh"},
	{rel: "Brindlemarsh/Season 4/Episode 05.mkv", ok: true, show: "Brindlemarsh", season: 4, episode: 5, showDir: "Brindlemarsh"},
	{rel: "Brindlemarsh/Specials/Brindlemarsh.S00E01.Marsh.Lights.mkv", ok: true, show: "Brindlemarsh", season: 0, episode: 1, title: "Marsh Lights", showDir: "Brindlemarsh"},
	{rel: "Brindlemarsh/Specials/Making Of.mkv", ok: true, show: "Brindlemarsh", season: 0, episode: 0, title: "Making Of", showDir: "Brindlemarsh"},
	{rel: "Brindlemarsh/S02/Brindlemarsh.S02E01.mkv", ok: true, show: "Brindlemarsh", season: 2, episode: 1, showDir: "Brindlemarsh"},
	{rel: "Quill Cops/Season 3/Quill.Cops.3x05.Il.Molo.Grigio.ITA.720p.WEB.x264-VCRXTEST.mkv", ok: true, show: "Quill Cops", season: 3, episode: 5, title: "Il Molo Grigio", showDir: "Quill Cops"},
	{rel: "Quill Cops/Season 3/Quill.Cops.3x01-3x02.Doppio.mkv", ok: true, show: "Quill Cops", season: 3, episode: 1, end: 2, title: "Doppio", showDir: "Quill Cops"},
	{rel: "Pellingham - Origins/Season 1/Pellingham.Origins.S01E03.1080p.WEB.H264-SAMPLEGRP.mkv", ok: true, show: "Pellingham - Origins", season: 1, episode: 3, showDir: "Pellingham - Origins"},
	{rel: "Orvel (US)/Season 1/Orvel.US.S01E01.Opening.Bell.720p.mkv", ok: true, show: "Orvel (US)", season: 1, episode: 1, title: "Opening Bell", showDir: "Orvel (US)"},
	{rel: "Harrowgate Desk/Season 2/Harrowgate.Desk.S02E01.Season.Two.Premiere.mkv", ok: true, show: "Harrowgate Desk", season: 2, episode: 1, title: "Season Two Premiere", showDir: "Harrowgate Desk"},
	// Show/file without season folders, and Show/Release/file.
	{rel: "Saltwick Patrol/Saltwick.Patrol.S01E01.German.DL.1080p.WEB.h264-VCRXTEST.mkv", ok: true, show: "Saltwick Patrol", season: 1, episode: 1, showDir: "Saltwick Patrol"},
	{rel: "Saltwick Patrol/Saltwick.Patrol.2022.S01E09.1080p.WEB-DL.SDR.AAC-VCRXTEST.mkv", ok: true, show: "Saltwick Patrol", season: 1, episode: 9, showDir: "Saltwick Patrol"},
	{rel: "Velloran Ninja/Velloran.Ninja.S04E04.135.480p.DVDrip.x264-VCRXTEST/Velloran.Ninja.S04E04.135.480p.DVDrip.x264-VCRXTEST.mkv", ok: true, show: "Velloran Ninja", season: 4, episode: 4, showDir: "Velloran Ninja"},
	{rel: "Quorvex at Ten/Quorvex.Late.2026.07.27.Guest.Name.1080p.WEB.h264-VCRXTEST.mkv", ok: true, show: "Quorvex at Ten", season: 2026, episode: 727, title: "Guest Name", date: "2026-07-27", showDir: "Quorvex at Ten"},
	{rel: "Quorvex at Ten/quorvex.late.2026.08.05.another.visitor.1080p.web.h264-vcrxtest.mkv", ok: true, show: "Quorvex at Ten", season: 2026, episode: 805, title: "another visitor", date: "2026-08-05", showDir: "Quorvex at Ten"},
	{rel: "Quorvex Island/Quorvex.Island.1983.1080p.E02.WEB-DL.AAC2.0.H.264-VCRXTEST.mkv", ok: true, show: "Quorvex Island", season: 1, episode: 2, showDir: "Quorvex Island"},
	// Loose episodes and release folders at the root.
	{rel: "Saltwick.Patrol.S01E03.Copper.Claws.1080p.WEB-DL.DD+2.0.x264-VCRXTEST.mkv", ok: true, show: "Saltwick Patrol", season: 1, episode: 3, title: "Copper Claws"},
	{rel: "Saltwick.Patrol.S01E03.Copper.Claws.1080p.WEB-DL.DD+2.0.x264-VCRXTEST/Saltwick.Patrol.S01E03.Copper.Claws.1080p.WEB-DL.DD+2.0.x264-VCRXTEST.mkv", ok: true, show: "Saltwick Patrol", season: 1, episode: 3, title: "Copper Claws"},
	{rel: "Saltwick.Patrol.S04.1080p.WEB-DL.x264-VCRXTEST/Saltwick.Patrol.S04E05.Kettle.Drum.1080p.mkv", ok: true, show: "Saltwick Patrol", season: 4, episode: 5, title: "Kettle Drum"},
	{rel: "Saltwick.Patrol.S05E09.Tidepool.SWESUB.1080p.WEB-DL.x264-VCRXTEST.mkv", ok: true, show: "Saltwick Patrol", season: 5, episode: 9, title: "Tidepool"},
	{rel: "Brindlemarsh S07E04 Brass Gale 1080p WEB-DL DDP5 1 H 264-VCRXTEST bonus 7/Brindlemarsh S07E04 Brass Gale 1080p WEB-DL DDP5 1 H 264-VCRXTEST.mkv", ok: true, show: "Brindlemarsh", season: 7, episode: 4, title: "Brass Gale"},
	{rel: "Brindlemarsh.S07E03.Copper.Tide.1080p.WEB-DL.DD5.1.H.264-VCRXTEST.1/abc.mkv", ok: true, show: "Brindlemarsh", season: 7, episode: 3},
	{rel: "Quill.Shop.2019.S01E04.Mindspace.720p.WEB-DL.DD+5.1.H.264-VCRXTEST.mkv", ok: true, show: "Quill Shop", showYear: 2019, season: 1, episode: 4, title: "Mindspace"},
	{rel: "4.Moons.Quorvex.S01E08.Northward.Bound.1080p.WEB-DL.DUAL.DDP5.1.Atmos.x264-VCRXTEST.mkv", ok: true, show: "4 Moons Quorvex", season: 1, episode: 8, title: "Northward Bound"},
	{rel: "Quorvex.Deep.S01E01.MULTi.720p.x265-VCRXTEST.mkv", ok: true, show: "Quorvex Deep", season: 1, episode: 1, title: ""},
	{rel: "QRX.2020.S01E01.1080p.BluRay.HEVC.mkv", ok: true, show: "QRX", showYear: 2020, season: 1, episode: 1},
	{rel: "The.Quillery.CA.2011.S03E08.Lender.Day.1080p.BluRay.x265-VCRXTEST.mkv", ok: true, show: "The Quillery CA", showYear: 2011, season: 3, episode: 8, title: "Lender Day"},
	{rel: "Pellingham-Origins.S03E07.MULTi.1080p.WEB-DL.H.264-VCRXTEST.mkv", ok: true, show: "Pellingham-Origins", season: 3, episode: 7},
	{rel: "Tracing.Quorvex.S02E04.Part2.1080p.WEB-DL.x264-VCRXTEST.mkv", ok: true, show: "Tracing Quorvex", season: 2, episode: 4, title: "Part2"},
	{rel: "Norsequill (2013) Season 1 E 7-9 1080p BluRay x264-VCRXTEST.mkv", ok: true, show: "Norsequill", showYear: 2013, season: 1, episode: 7, end: 9},
	{rel: "[VcrxSubs] Saltwick Patrol - 07 [1080p].mkv", ok: true, show: "Saltwick Patrol", season: 1, episode: 7},
	{rel: "quorvex.late.2018.11.20.Some.Visitor.720p.HDTV.x264-VCRXTEST/quorvex.late.2018.11.20.Some.Visitor.720p.HDTV.x264-VCRXTEST.mkv", ok: true, show: "quorvex late", season: 2018, episode: 1120, date: "2018-11-20", title: "Some Visitor"},
	// A file with no number at all becomes an unnumbered special.
	{rel: "QRX40.Tidewater.Gala.2025.1080p.WEB.h264-VCRXTEST/QRX40.Tidewater.Gala.2025.1080p.WEB.h264-VCRXTEST.mkv", ok: true, show: "QRX40 Tidewater Gala", showYear: 2025, season: 0, episode: 0, title: "QRX40 Tidewater Gala"},
	// Resolution numbers are not episodes.
	{rel: "Brindlemarsh/Season 1/Brindlemarsh.S01E05.1920x1080.mkv", ok: true, show: "Brindlemarsh", season: 1, episode: 5, showDir: "Brindlemarsh"},
	// Posting and client wrappers.
	{rel: "Brindlemarsh/Season 13/[1+8] Brindlemarsh.S13E09.Snowbound.1080p.WEB-DL.mkv", ok: true, show: "Brindlemarsh", season: 13, episode: 9, title: "Snowbound", showDir: "Brindlemarsh"},
	{rel: "Quorvex Haven Pack 2 [13+42] - Quorvex.Haven.S01E06.Copper.Room.1080p.WEBRip.x265.mkv", ok: true, show: "Quorvex Haven", season: 1, episode: 6, title: "Copper Room"},
	{rel: "_UNPACK_Quorvex.Deep.S04E01.1080p.WEB.h264-VCRXTEST/Quorvex.Deep.S04E01.1080p.WEB.h264-VCRXTEST.mkv", ok: true, show: "Quorvex Deep", season: 4, episode: 1},
	{rel: "Salt & Quills (2020)/Season 7/Salt.and.Quills.2020.S07E06.Lantern.Bearers.1080p.mkv", ok: true, show: "Salt & Quills", showYear: 2020, season: 7, episode: 6, title: "Lantern Bearers", showDir: "Salt & Quills (2020)"},
	{rel: "Quill Cops/Season 2/Quill.Cops.S02E02.MULTI.1080p.WEB.H264-VCRXTEST.mkv", ok: true, show: "Quill Cops", season: 2, episode: 2, showDir: "Quill Cops"},
	// Movie libraries accept only the unambiguous forms.
	{rel: "Quillmoor (2026) S01 (1080p WEB-DL x265 10bit AAC VCRXTEST)/Quillmoor (2026) - S01E04 - Brass, Copper and Tin (1080p WEB-DL x265 VCRXTEST).mkv", strict: true, ok: true, show: "Quillmoor", showYear: 2026, season: 1, episode: 4, title: "Brass, Copper and Tin"},
	{rel: "Velloran (1996)/Season 1/Quorvex Chronicles in Sepia - S01E05 - Copper Dawn.mkv", strict: true, ok: true, show: "Quorvex Chronicles in Sepia", season: 1, episode: 5, title: "Copper Dawn"},
	{rel: "Tidewater.Quill.2022.1080p.WEBRip.HEVC.10Bit-VCRXTEST/Tidewater Quill S01E05 Uphill Pedal.mkv", strict: true, ok: true, show: "Tidewater Quill", showYear: 2022, season: 1, episode: 5, title: "Uphill Pedal"},
	{rel: "Quill.Call.US.S02E02.720p.HDTV.X264-VCRXTEST/Quill.Call.US.S02E02.720p.HDTV.x264-VCRXTEST.mkv", strict: true, ok: true, show: "Quill Call US", season: 2, episode: 2},
	{rel: "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-VCRXTEST/notld.mkv", strict: true, ok: false},
	{rel: "Quorvex 1963 2x4 Edition.mkv", strict: true, ok: false},
	{rel: "Quorvex.Late.2026.07.27.mkv", strict: true, ok: false},
	{rel: "Velloran.1927.1080p.E02.mkv", strict: true, ok: false},
	{rel: "Quorvex.Range.1996.5.1x264.mkv", strict: true, ok: false},
	{rel: "S3ven.Quills.1995.1080p.mkv", strict: true, ok: false},
}

func TestParseEpisodePath(t *testing.T) {
	for _, c := range episodeCases {
		t.Run(c.rel, func(t *testing.T) {
			ep, ok := ParseEpisodePath(c.rel, c.strict)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (%+v)", ok, c.ok, ep)
			}
			if !ok {
				return
			}
			if ep.Show != c.show || ep.ShowYear != c.showYear {
				t.Errorf("show = %q (%d), want %q (%d)", ep.Show, ep.ShowYear, c.show, c.showYear)
			}
			if ep.Season != c.season || ep.Episode != c.episode || ep.EpisodeEnd != c.end {
				t.Errorf("numbers = S%dE%d-%d, want S%dE%d-%d", ep.Season, ep.Episode, ep.EpisodeEnd, c.season, c.episode, c.end)
			}
			if ep.Title != c.title {
				t.Errorf("title = %q, want %q", ep.Title, c.title)
			}
			if ep.AirDate != c.date {
				t.Errorf("date = %q, want %q", ep.AirDate, c.date)
			}
			if ep.ShowDir != c.showDir {
				t.Errorf("show dir = %q, want %q", ep.ShowDir, c.showDir)
			}
		})
	}
}

func TestVersionPrefix(t *testing.T) {
	cases := []struct{ base, folder, prefix, label string }{
		{"Night of the Living Dead (1968) - 1080p", "Night of the Living Dead (1968)", "Night of the Living Dead (1968)", "1080p"},
		{"Night of the Living Dead (1968) - Colorized", "Night of the Living Dead (1968)", "Night of the Living Dead (1968)", "Colorized"},
		{"Night of the Living Dead (1968) - Director's Cut", "Other Folder", "Night of the Living Dead (1968)", "Director's Cut"},
		{"Night of the Living Dead (1968) - 4K HDR REMUX", "Other Folder", "Night of the Living Dead (1968)", "4K HDR REMUX"},
		{"Quorvex Harbor - Low Tide (2018)", "Quorvex Harbor - Low Tide (2018)", "Quorvex Harbor - Low Tide (2018)", ""},
		{"Quorvex Harbor - Low Tide (2018) - 1080p", "Quorvex Harbor - Low Tide (2018)", "Quorvex Harbor - Low Tide (2018)", "1080p"},
		{"Quorvex Harbor - Low Tide (2018)", "Movies", "Quorvex Harbor - Low Tide (2018)", ""},
		{"Night.of.the.Living.Dead.1968.1080p", "x", "Night.of.the.Living.Dead.1968.1080p", ""},
		{"Nosferatu (1922) - 1080p - Extended", "Nosferatu (1922)", "Nosferatu (1922)", "1080p - Extended"},
	}
	for _, c := range cases {
		p, l := versionPrefix(c.base, c.folder)
		if p != c.prefix || l != c.label {
			t.Errorf("versionPrefix(%q, %q) = %q, %q; want %q, %q", c.base, c.folder, p, l, c.prefix, c.label)
		}
	}
}

func TestNormalizeAndSort(t *testing.T) {
	norm := map[string]string{
		"Salt & Quills":        "salt and quills",
		"Orvel's Quills":       "orvels quills",
		"Cascadilla - Origins": "cascadilla origins",
		"Cascadilla-Origins":   "cascadilla origins",
		"Pellé Café":           "pelle cafe",
		"  D.O.A.  ":           "d o a",
	}
	for in, want := range norm {
		if got := NormalizeTitle(in); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
	sorts := map[string]string{
		"The General": "General, The", "A Trip to Velloran": "Trip to Velloran, A", "An Evening": "Evening, An",
		"Theater": "Theater", "The": "The", "Anvilbrook": "Anvilbrook", "Nosferatu": "Nosferatu",
	}
	for in, want := range sorts {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestObfuscated(t *testing.T) {
	for _, s := range []string{"0f3c9a7e5b1d4c2a8e6f9b0d7c5a3e1f", "kQ7xT2mZp9LwR4vNcY8b", "j5RtWq2NzX8pA"} {
		if !obfuscated(s) {
			t.Errorf("obfuscated(%q) = false", s)
		}
	}
	for _, s := range []string{"Nosferatu", "Night.of.the.Living.Dead.1968", "VCRXTESTRELEASE", "123456789012"} {
		if obfuscated(s) {
			t.Errorf("obfuscated(%q) = true", s)
		}
	}
}

func TestSkipFile(t *testing.T) {
	big := int64(4 << 30)
	cases := []struct {
		name string
		size int64
		want string
	}{
		{"._Night.of.the.Living.Dead.1968.mkv", big, "appledouble"},
		{".Night.of.the.Living.Dead.1968.mkv", big, "hidden"},
		{"Night.of.the.Living.Dead.1968.nfo", 100, "not video"},
		{"Night.of.the.Living.Dead.1968.iso", big, "not video"},
		{"vcrxtest-notld.rar", big, "not video"},
		{"vcrxtest-notld.r00", big, "not video"},
		{"vcrxtest-notld.part01.rar", big, "not video"},
		{"Night.of.the.Living.Dead.1968.mkv.part", big, "not video"},
		{"VTS_01_1.VOB", big, "not video"},
		{"movie.divx", big, "not video"},
		{"sample-notld.1968.1080p.mkv", 50 << 20, "sample"},
		{"notld.1968.1080p.sample.mkv", 50 << 20, "sample"},
		{"Night of the Living Dead 1968 BRRip x264 720p-VCRXTEST-Sample.mp4", 50 << 20, "sample"},
		{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-VCRXTEST.sample.mkv", big, "sample"},
		{"SAMPLE.avi", big, "sample"},
		{"SaMple.avi", 10 << 20, "sample"},
		{"Velloran Wake sample.mp4", 30 << 20, "sample"},
		{"s-q7vx-sample.mkv", 20 << 20, "sample"},
		{"Samples of VCRX (2010).mkv", big, ""},
		{"The Sample (2020).mkv", big, ""},
		{"The Sample (2020).mkv", 40 << 20, "sample"},
		{"Night of the Living Dead (1968)-trailer.mkv", 90 << 20, "extras"},
		{"trailer.mp4", 90 << 20, "extras"},
		{"Night.of.the.Living.Dead.1968.1080p.mkv", big, ""},
		{"NOTLD.MKV", big, ""},
		{"00030.m2ts", big, ""},
		{"clip.webm", big, ""},
		{"clip.mpg", big, ""},
		{"clip.wmv", big, ""},
		{"clip.ogv", big, ""},
		{"clip.flv", big, ""},
		{"clip.ts", big, ""},
		{"clip.mts", big, ""},
		{"clip.mov", big, ""},
		{"clip.m4v", big, ""},
		{"Quorvex.S01E01.zip.mkv", big, ""},
	}
	for _, c := range cases {
		if got := SkipFile(c.name, c.size); got != c.want {
			t.Errorf("SkipFile(%q, %d) = %q, want %q", c.name, c.size, got, c.want)
		}
	}
}

func TestSkipDir(t *testing.T) {
	cases := []struct{ name, kind, want string }{
		{"Sample", KindMovie, "sample"}, {"SAMPLE", KindShow, "sample"}, {"samples", KindMovie, "sample"},
		{"Extras", KindMovie, "extras"}, {"Featurettes", KindMovie, "extras"}, {"Behind The Scenes", KindMovie, "extras"},
		{"Behind.The.Scenes", KindShow, "extras"}, {"Deleted Scenes", KindMovie, "extras"}, {"Interviews", KindMovie, "extras"},
		{"Trailers", KindMovie, "extras"}, {"Shorts", KindMovie, "extras"},
		{"Specials", KindMovie, "extras"}, {"Specials", KindShow, ""},
		{"@eaDir", KindMovie, "system"}, {"#recycle", KindMovie, "system"}, {"lost+found", KindShow, "system"},
		{"BDMV", KindMovie, "system"}, {"VIDEO_TS", KindMovie, "system"},
		{".AppleDouble", KindMovie, "hidden"}, {".Trashes", KindShow, "hidden"},
		{"Season 1", KindShow, ""}, {"Subs", KindMovie, ""}, {"Samples of VCRX (2010)", KindMovie, ""},
		{"Night of the Living Dead (1968)", KindMovie, ""},
	}
	for _, c := range cases {
		if got := SkipDir(c.name, c.kind); got != c.want {
			t.Errorf("SkipDir(%q, %s) = %q, want %q", c.name, c.kind, got, c.want)
		}
	}
}

func TestSubtitleTags(t *testing.T) {
	cases := []struct {
		rest, lang, title     string
		forced, hi, isDefault bool
	}{
		{".en", "eng", "", false, false, false},
		{".eng", "eng", "", false, false, false},
		{".English", "eng", "", false, false, false},
		{".eng.forced", "eng", "", true, false, false},
		{".en.forced", "eng", "", true, false, false},
		{".English.sdh", "eng", "", false, true, false},
		{".en.hi", "eng", "", false, true, false},
		{".hi", "hin", "", false, false, false},
		{".en.cc", "eng", "", false, true, false},
		{".fr.default", "fre", "", false, false, true},
		{".fra", "fre", "", false, false, false},
		{".de", "ger", "", false, false, false},
		{".deu.forced", "ger", "", true, false, false},
		{".pt-BR", "por", "", false, false, false},
		{".zh-Hans", "chi", "", false, false, false},
		{".es-419", "spa", "", false, false, false},
		{".it", "ita", "", false, false, false},
		{".no", "nor", "", false, false, false},
		{".nl", "dut", "", false, false, false},
		{".Commentary.en", "eng", "Commentary", false, false, false},
		{".2", "", "", false, false, false},
		{"", "", "", false, false, false},
		{"2_English", "eng", "", false, false, false},
		{"3_French", "fre", "", false, false, false},
		{"14_Brazilian", "por", "", false, false, false},
	}
	for _, c := range cases {
		var sc Sidecar
		applySubtitleTags(&sc, c.rest)
		got := [5]any{sc.Language, sc.Title, sc.Forced, sc.HearingImpaired, sc.Default}
		want := [5]any{c.lang, c.title, c.forced, c.hi, c.isDefault}
		if got != want {
			t.Errorf("applySubtitleTags(%q) = %v, want %v", c.rest, got, want)
		}
	}
}

func TestComposeLabel(t *testing.T) {
	cases := []struct {
		explicit, edition string
		tags              MediaTags
		want              string
	}{
		{"Colorized", "Extended", MediaTags{Resolution: "1080p"}, "Colorized"},
		{"", "", MediaTags{Resolution: "4K", HDR: []string{"HDR"}, Source: "REMUX"}, "4K HDR REMUX"},
		{"", "", MediaTags{Resolution: "1080p", Source: "BluRay"}, "1080p BluRay"},
		{"", "Director's Cut", MediaTags{}, "Director's Cut"},
		{"", "", MediaTags{Resolution: "1080p", ThreeD: true}, "1080p 3D"},
		{"", "", MediaTags{}, ""},
	}
	for _, c := range cases {
		if got := composeLabel(c.explicit, c.edition, c.tags, nil); got != c.want {
			t.Errorf("composeLabel(%q, %q, %+v) = %q, want %q", c.explicit, c.edition, c.tags, got, c.want)
		}
	}
}

// TestFixtureCount keeps the parser tables from shrinking unnoticed.
func TestFixtureCount(t *testing.T) {
	n := len(movieNameCases) + len(moviePathCases) + len(episodeCases)
	if n < 150 {
		t.Fatalf("%d parser fixtures, want at least 150", n)
	}
	t.Logf("%d movie name, %d movie path, %d episode fixtures (%d total)", len(movieNameCases), len(moviePathCases), len(episodeCases), n)
}
