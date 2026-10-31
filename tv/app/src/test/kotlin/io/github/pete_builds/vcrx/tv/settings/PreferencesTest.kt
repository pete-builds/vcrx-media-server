package io.github.pete_builds.vcrx.tv.settings

import io.github.pete_builds.vcrx.tv.caps.CapabilityBuilder
import io.github.pete_builds.vcrx.tv.caps.CodecNames
import io.github.pete_builds.vcrx.tv.net.AudioCodecCapability
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.net.VideoCodecCapability
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Stored settings, language matching, Home row layout and the words Settings shows. */
class PreferencesTest {
    @Test
    fun theDefaultsAreTheAppsBehaviourBeforeSettingsExisted() {
        val p = Preferences()
        assertTrue(p.subtitles.enabled)
        assertEquals(SubtitleMode.ForeignAudio, p.subtitles.mode)
        assertFalse("the TV's caption settings until the viewer picks Custom", p.subtitles.style.custom)
        assertTrue(p.playback.autoPlayNext)
        assertEquals(10, p.playback.countdownSeconds)
        assertEquals(ResumeMode.Resume, p.playback.resume)
        assertEquals(10, p.playback.skipBackSeconds)
        assertEquals(10, p.playback.skipForwardSeconds)
        assertEquals(QualityCap.Original, p.playback.quality)
        assertTrue(p.audio.preferOriginal)
        assertTrue(p.audio.passthrough)
        assertTrue(p.video.hdr && p.video.dolbyVision)
    }

    @Test
    fun aSavedSettingRoundTrips() {
        val p = Preferences(
            subtitles = SubtitlePrefs(enabled = false, mode = SubtitleMode.ForcedOnly, languages = listOf("fr", "en"),
                style = SubtitleStyle(custom = true, size = SubtitleSize.Huge, color = SubtitleColor.Yellow)),
            playback = PlaybackPrefs(autoPlayNext = false, quality = QualityCap.Hd4, resume = ResumeMode.Ask),
            audio = AudioPrefs(language = "ja", preferOriginal = false, passthrough = false),
            video = VideoPrefs(dolbyVision = false),
            home = HomePrefs(hidden = listOf("recently_added_movies"), order = listOf("continue_watching")),
            trailers = TrailerMode.Off,
            skipIntro = SkipIntroMode.Automatic,
        )
        assertEquals(p, Preferences.decode(Preferences.encode(p)))
    }

    @Test
    fun aSettingFromAnotherBuildOrBrokenStorageNeverFails() {
        assertEquals(Preferences(), Preferences.decode(null))
        assertEquals(Preferences(), Preferences.decode("not json"))
        // A newer build's fields are ignored; an enum value this build lacks falls back to the default.
        val newer = """{"subtitles":{"enabled":false,"mode":"Karaoke","future":1},"playback":{"quality":"Hd4"},"unknown":{}}"""
        val p = Preferences.decode(newer)
        assertFalse(p.subtitles.enabled)
        assertEquals(SubtitleMode.ForeignAudio, p.subtitles.mode)
        assertEquals(QualityCap.Hd4, p.playback.quality)
        // An older build's partial file keeps everything else at the defaults.
        assertEquals(Preferences(audio = AudioPrefs(passthrough = false)), Preferences.decode("""{"audio":{"passthrough":false}}"""))
    }

    @Test
    fun languageCodesCompareAcrossTheirForms() {
        assertEquals("en", Languages.base("eng"))
        assertEquals("en", Languages.base("EN-us"))
        assertEquals("fr", Languages.base("fre"))
        assertEquals("fr", Languages.base("fra"))
        assertEquals("de", Languages.base("ger"))
        assertEquals("pt", Languages.base("pt_BR"))
        assertNull(Languages.base("und"))
        assertNull(Languages.base(""))
        assertNull(Languages.base(null))
        assertEquals("tlh", Languages.base("tlh"))
        assertTrue(Languages.same("eng", "en"))
        assertFalse(Languages.same(null, null))
        assertEquals("French", Languages.name("fre"))
        assertEquals(listOf("en"), Languages.preferred(emptyList(), "en"))
        assertEquals(listOf("fr", "en"), Languages.preferred(listOf("fre", "eng", "en"), "de"))
    }

    @Test
    fun homeRowsFollowTheViewersOrderAndHiddenRowsAreLeftOut() {
        val rows = listOf("continue_watching", "next_up", "recent:1", "recent:2")
        val prefs = HomePrefs(hidden = listOf("next_up"), order = listOf("recent:2", "continue_watching"))
        assertEquals(listOf("recent:2", "continue_watching", "recent:1"), HomeLayout.arrange(rows, prefs) { it })
        // A row the viewer has never placed (a new library) keeps the server's order after the placed ones.
        assertEquals(
            listOf("recent:2", "continue_watching", "recent:3", "recent:1"),
            HomeLayout.arrange(listOf("recent:3", "continue_watching", "recent:1", "recent:2"), HomePrefs(order = listOf("recent:2", "continue_watching"))) { it },
        )
        assertEquals(rows, HomeLayout.arrange(rows, HomePrefs()) { it })
    }

    @Test
    fun movingAndHidingHomeRows() {
        val rows = listOf("a", "b", "c")
        val down = HomeLayout.move(rows, HomePrefs(), "a", 1)
        assertEquals(listOf("b", "a", "c"), HomeLayout.arrange(rows, down) { it })
        val up = HomeLayout.move(rows, down, "c", -1)
        assertEquals(listOf("b", "c", "a"), HomeLayout.arrange(rows, up) { it })
        // The ends stay put.
        assertEquals(listOf("b", "c", "a"), HomeLayout.arrange(rows, HomeLayout.move(rows, up, "b", -1)) { it })
        val hidden = HomeLayout.toggle(up, "c")
        assertEquals(listOf("b", "a"), HomeLayout.arrange(rows, hidden) { it })
        assertEquals(listOf("b", "c", "a"), HomeLayout.ordered(rows, hidden))
        assertEquals(listOf("b", "c", "a"), HomeLayout.arrange(rows, HomeLayout.toggle(hidden, "c")) { it })
    }

    @Test
    fun whatThisTvDecodesReadsForAPerson() {
        val caps = Capabilities(
            containers = listOf("mkv"),
            videoCodecs = listOf(VideoCodecCapability("hevc", maxBitDepth = 10, maxWidth = 3840, maxHeight = 2160), VideoCodecCapability("h264", maxHeight = 1080)),
            audioCodecs = listOf(
                AudioCodecCapability("aac", 8, CapabilityBuilder.HARDWARE),
                AudioCodecCapability("eac3", 6, CapabilityBuilder.PASSTHROUGH),
            ),
            hdrTypes = listOf(CodecNames.HDR10, CodecNames.DOLBY_VISION),
            maxWidth = 3840,
            maxHeight = 2160,
        )
        val lines = Describe.decoding(caps).toMap()
        assertEquals("HEVC (H.265) up to 4K, 10-bit\nH.264 up to 1080p", lines["Video"])
        assertEquals("HDR10, Dolby Vision", lines["HDR"])
        assertEquals("Dolby Digital Plus (5.1)", lines["Sent to the receiver"])
        assertEquals("AAC", lines["Audio decoded here"])
        assertEquals("3840 x 2160 (4K)", lines["Screen"])
        assertNull(lines["Quality limit"])
        val plain = Describe.decoding(caps.copy(hdrTypes = emptyList(), audioCodecs = emptyList(), maxBitrate = 8_000_000)).toMap()
        assertEquals("None (HDR is converted to SDR)", plain["HDR"])
        assertEquals("Nothing; the TV decodes all audio itself", plain["Sent to the receiver"])
        assertEquals("Up to 8.0 Mbps", plain["Quality limit"])
    }

    @Test
    fun theSpeedTestAnswerIsInMegabitsPerSecond() {
        assertEquals("320 Mbps (48 MB in 1.2 s)", Describe.speed(48_000_000, 1_200))
        assertEquals("6.4 Mbps (8 MB in 10.0 s)", Describe.speed(8_000_000, 10_000))
        // A zero time (a cached or instant answer) is not a division by zero.
        assertTrue(Describe.speed(1_000, 0).endsWith("in 0.0 s)"))
    }

    @Test
    fun settingsWordsForEveryValue() {
        assertEquals("Original (no limit)", Describe.quality(QualityCap.Original))
        assertEquals("1080p, up to 20 Mbps", Describe.quality(QualityCap.Fhd20))
        assertEquals("4K, up to 60 Mbps", Describe.quality(QualityCap.Uhd4k))
        assertEquals("1 min", Describe.seconds(60))
        assertEquals("15 s", Describe.seconds(15))
        assertEquals("Same as the TV (English)", Describe.languages(emptyList(), "en"))
        assertEquals("French, English", Describe.languages(listOf("fr", "en"), "en"))
        assertEquals("50% black", Describe.background(SubtitleBackground.Medium))
        for (mode in SubtitleMode.entries) assertTrue(Describe.modeHelp(mode).isNotBlank())
    }
}
