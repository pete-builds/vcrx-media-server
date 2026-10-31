package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.settings.Preferences
import io.github.pete_builds.vcrx.tv.settings.SubtitleMode
import io.github.pete_builds.vcrx.tv.settings.SubtitlePrefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Which subtitle track a title opens with, under every mode, language and
 * remembered choice. A null answer is subtitles off.
 */
class SubtitlePickerTest {
    private val engFull = SubtitleCandidate("eng", "eng")
    private val engSdh = SubtitleCandidate("eng-sdh", "eng", label = "SDH")
    private val engForced = SubtitleCandidate("eng-forced", "eng", forced = true)
    private val fre = SubtitleCandidate("fre", "fre")
    private val freForced = SubtitleCandidate("fre-forced", "fre", forced = true)
    private val spa = SubtitleCandidate("spa", "es")
    private val untagged = SubtitleCandidate("und", null, isDefault = true)
    private val all = listOf(engFull, engSdh, engForced, fre, freForced, spa)

    private fun prefs(enabled: Boolean = true, mode: SubtitleMode = SubtitleMode.Always, vararg languages: String) =
        SubtitlePrefs(enabled = enabled, mode = mode, languages = languages.toList())

    private fun choose(
        prefs: SubtitlePrefs,
        candidates: List<SubtitleCandidate> = all,
        audio: String? = "eng",
        sticky: StickyChoice? = null,
        device: String = "en",
    ) = SubtitlePicker.choose(prefs, candidates, audio, sticky, device)

    @Test
    fun offMeansNoTrackAtAllForcedIncluded() {
        for (mode in SubtitleMode.entries) {
            assertNull(mode.name, choose(prefs(enabled = false, mode = mode)))
            assertNull(mode.name, choose(prefs(enabled = false, mode = mode), audio = "jpn"))
        }
    }

    @Test
    fun alwaysPicksTheFullTrackInTheFirstPreferredLanguage() {
        assertEquals(engFull, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en"))))
        assertEquals(fre, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("fr", "en"))))
        // A three-letter preference matches two-letter tags and the reverse.
        assertEquals(spa, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("spa"))))
        assertEquals(fre, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("fra"))))
    }

    @Test
    fun theSecondLanguageIsUsedOnlyWhenTheFirstIsMissing() {
        val noFrench = listOf(engFull, spa)
        assertEquals(engFull, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("fr", "en")), noFrench))
        assertNull(choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("de", "it")), noFrench))
    }

    @Test
    fun noPreferenceMeansTheTvsLanguage() {
        assertEquals(engFull, choose(prefs(mode = SubtitleMode.Always), device = "en"))
        assertEquals(fre, choose(prefs(mode = SubtitleMode.Always), device = "fr"))
    }

    @Test
    fun theFilesDefaultWinsWithinALanguage() {
        val sdhDefault = engSdh.copy(isDefault = true)
        assertEquals(sdhDefault, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), listOf(engFull, sdhDefault)))
    }

    @Test
    fun alwaysFallsBackToForcedThenToAnUntaggedDefault() {
        assertEquals(engForced, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), listOf(engForced, fre)))
        assertEquals(untagged, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), listOf(untagged, fre)))
        assertNull(choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), listOf(untagged.copy(isDefault = false))))
    }

    @Test
    fun forcedOnlyNeverPicksAFullTrack() {
        val p = prefs(mode = SubtitleMode.ForcedOnly, languages = arrayOf("en"))
        assertEquals(engForced, choose(p))
        assertEquals(engForced, choose(p, audio = "jpn"))
        assertNull(choose(p, listOf(engFull, fre, freForced)))
    }

    @Test
    fun foreignAudioShowsFullSubtitlesOnlyWhenTheAudioIsNotMine() {
        val p = prefs(mode = SubtitleMode.ForeignAudio, languages = arrayOf("en"))
        assertEquals(engFull, choose(p, audio = "jpn"))
        assertEquals(engFull, choose(p, audio = "fr"))
        // My language: forced lines only.
        assertEquals(engForced, choose(p, audio = "eng"))
        assertEquals(engForced, choose(p, audio = "en-US"))
        assertNull(choose(p, listOf(engFull, fre), audio = "eng"))
        // Foreign audio and no full track in my language: forced in my language, else nothing.
        assertEquals(engForced, choose(p, listOf(engForced, fre), audio = "jpn"))
        assertNull(choose(p, listOf(fre, spa), audio = "jpn"))
    }

    @Test
    fun foreignAudioTreatsUntaggedAudioAsMine() {
        val p = prefs(mode = SubtitleMode.ForeignAudio, languages = arrayOf("en"))
        assertEquals(engForced, choose(p, audio = null))
        assertEquals(engForced, choose(p, audio = "und"))
        assertTrue(SubtitlePicker.audioIsMine(null, listOf("en")))
        assertFalse(SubtitlePicker.audioIsMine("ita", listOf("en")))
        // Audio in the second preferred language is still mine.
        assertEquals(freForced, choose(prefs(mode = SubtitleMode.ForeignAudio, languages = arrayOf("en", "fr")), listOf(engFull, freForced), audio = "fre"))
    }

    @Test
    fun unsupportedTracksAreNeverChosen() {
        val broken = engFull.copy(supported = false)
        assertEquals(engSdh, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), listOf(broken, engSdh)))
        assertNull(choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), listOf(broken)))
        assertNull(SubtitlePicker.forToggleOn(prefs(), listOf(broken), "en"))
    }

    @Test
    fun aChoiceMadeWhileWatchingOutranksSettings() {
        // Turned on while watching: on in the next file even with Settings off or forced-only.
        val sticky = SubtitlePicker.sticky(fre)
        assertEquals(fre, choose(prefs(enabled = false), sticky = sticky))
        assertEquals(fre, choose(prefs(mode = SubtitleMode.ForcedOnly, languages = arrayOf("en")), sticky = sticky))
        // Turned off while watching: off even with Settings on Always.
        assertNull(choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), sticky = StickyChoice(on = false)))
    }

    @Test
    fun aRememberedTrackIsFoundByWhatItIsNotWhereItSits() {
        // The next episode lists its tracks in another order; the SDH English one is still found.
        val nextEpisode = listOf(spa, engSdh.copy(id = "x1"), engFull.copy(id = "x2"), engForced.copy(id = "x3"))
        assertEquals("x1", choose(prefs(), nextEpisode, sticky = SubtitlePicker.sticky(engSdh))?.id)
        assertEquals("x3", choose(prefs(), nextEpisode, sticky = SubtitlePicker.sticky(engForced))?.id)
    }

    @Test
    fun aRememberedLanguageMissingFromTheFileFallsBackToSettings() {
        val sticky = SubtitlePicker.sticky(fre)
        assertEquals(engFull, choose(prefs(mode = SubtitleMode.Always, languages = arrayOf("en")), listOf(engFull, spa), sticky = sticky))
        assertNull(choose(prefs(enabled = false), listOf(engFull, spa), sticky = sticky))
    }

    @Test
    fun theTogglePicksTheBestTrackWhateverTheMode() {
        val p = prefs(mode = SubtitleMode.ForcedOnly, languages = arrayOf("en"))
        assertEquals(engFull, SubtitlePicker.forToggleOn(p, all, "en"))
        assertEquals(engForced, SubtitlePicker.forToggleOn(p, listOf(engForced, fre), "en"))
        // Nothing in my languages: the first full track rather than nothing, since the viewer asked.
        assertEquals(fre, SubtitlePicker.forToggleOn(p, listOf(freForced, fre, spa), "en"))
        assertEquals(freForced, SubtitlePicker.forToggleOn(p, listOf(freForced), "en"))
        assertNull(SubtitlePicker.forToggleOn(p, emptyList(), "en"))
    }

    @Test
    fun choicesStickPerTitleAndAcrossAShow() {
        val memory = SubtitleMemory()
        memory.remember(itemId = 101, showId = 7, choice = SubtitlePicker.sticky(fre))
        assertEquals(SubtitlePicker.sticky(fre), memory.lookup(101, 7))
        // The next episode of the same show, watched in a row, opens with the same choice.
        assertEquals(SubtitlePicker.sticky(fre), memory.lookup(102, 7))
        assertEquals(fre, choose(prefs(enabled = false), sticky = memory.lookup(102, 7)))
        // Another show and a movie are untouched.
        assertNull(memory.lookup(201, 8))
        assertNull(memory.lookup(300, null))
        // Turning them off in episode 2 sticks to the show from then on, but episode 1 keeps its own choice.
        memory.remember(itemId = 102, showId = 7, choice = StickyChoice(on = false))
        assertEquals(StickyChoice(on = false), memory.lookup(103, 7))
        assertEquals(SubtitlePicker.sticky(fre), memory.lookup(101, 7))
    }

    @Test
    fun aMovieChoiceSticksToThatMovieOnly() {
        val memory = SubtitleMemory()
        memory.remember(itemId = 300, showId = null, choice = SubtitlePicker.sticky(engFull))
        assertEquals(SubtitlePicker.sticky(engFull), memory.lookup(300, null))
        assertNull(memory.lookup(301, null))
    }

    @Test
    fun theMemoryIsCappedOldestFirst() {
        val memory = SubtitleMemory(max = 3)
        for (id in 1L..5L) memory.remember(id, null, StickyChoice(on = true, language = "en"))
        assertEquals(3, memory.size)
        assertNull(memory.lookup(1, null))
        assertNull(memory.lookup(2, null))
        assertEquals("en", memory.lookup(5, null)?.language)
        // Using an old entry again makes it the newest.
        memory.remember(3, null, StickyChoice(on = false))
        memory.remember(6, null, StickyChoice(on = true))
        assertEquals(StickyChoice(on = false), memory.lookup(3, null))
        assertNull(memory.lookup(4, null))
    }

    @Test
    fun changingTheSubtitleDefaultsInSettingsForgetsChoicesMadeWhileWatching() {
        val base = Preferences()
        fun changed(subs: SubtitlePrefs) = Preferences.resetsSubtitleChoices(base, base.copy(subtitles = subs))
        assertTrue(changed(base.subtitles.copy(enabled = false)))
        assertTrue(changed(base.subtitles.copy(mode = SubtitleMode.Always)))
        assertTrue(changed(base.subtitles.copy(languages = listOf("fr"))))
        // Style and other pages leave them alone.
        assertFalse(changed(base.subtitles.copy(style = base.subtitles.style.copy(custom = true))))
        assertFalse(Preferences.resetsSubtitleChoices(base, base.copy(playback = base.playback.copy(autoPlayNext = false))))
    }

    @Test
    fun theStickyRecordIsTheTracksIdentity() {
        assertEquals(StickyChoice(on = true, language = "en", forced = true, label = null), SubtitlePicker.sticky(engForced))
        assertEquals(StickyChoice(on = true, language = "en", forced = false, label = "SDH"), SubtitlePicker.sticky(engSdh))
        assertEquals(StickyChoice(on = false), SubtitlePicker.sticky(null))
    }
}
