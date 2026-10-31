package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.Marker
import io.github.pete_builds.vcrx.tv.net.PlaybackSession
import io.github.pete_builds.vcrx.tv.net.PlaybackSettings
import io.github.pete_builds.vcrx.tv.settings.SkipIntroMode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SkipMarkersTest {
    private val duration = 1_500_000L
    private val intro = Marker("intro", 60_000, 95_000, 0.9, "detected")
    private val creditsToEnd = Marker("credits", 1_420_000, duration, 0.85, "detected")
    private val button = PlaybackSettings.BUTTON

    private fun at(pos: Long, vararg ms: Marker, hasNext: Boolean = true, mode: String = button) =
        SkipMarkers.actionAt(ms.toList(), pos, duration, hasNext, mode)

    @Test
    fun theButtonShowsOnlyInsideAMarker() {
        assertNull(at(59_999, intro))
        assertEquals("Skip Intro", at(60_000, intro)?.label)
        assertEquals("Skip Intro", at(94_000, intro)?.label)
        // The last half second is left alone, so the button does not flash as the intro ends.
        assertNull(at(94_600, intro))
        assertNull(at(95_000, intro))
        assertNull(at(200_000, intro, creditsToEnd))
    }

    @Test
    fun skipIntroSeeksToItsEnd() {
        val a = at(70_000, intro)!!
        assertEquals(95_000L, a.targetMs)
        assertFalse(a.playsNext)
    }

    @Test
    fun creditsToTheEndGoStraightToUpNext() {
        val a = at(1_430_000, intro, creditsToEnd)!!
        assertEquals("Next Episode", a.label)
        assertTrue(a.playsNext)
        // The last episode: there is nothing next, so the credits are skipped to the end.
        val last = at(1_430_000, creditsToEnd, hasNext = false)!!
        assertEquals("Skip Credits", last.label)
        assertFalse(last.playsNext)
        assertEquals(duration, last.targetMs)
    }

    @Test
    fun aPostCreditsSceneIsNeverSkipped() {
        // The server ends credits before a scene that follows them.
        val credits = Marker("credits", 1_400_000, 1_460_000, 0.8, "detected")
        val a = at(1_410_000, credits)!!
        assertEquals("Skip Credits", a.label)
        assertFalse(a.playsNext)
        assertEquals(1_460_000L, a.targetMs)
        assertNull(at(1_465_000, credits))
    }

    @Test
    fun automaticOnlyWhenTheServerSaysAndTheUserChoseIt() {
        val sure = intro.copy(autoSkip = true)
        assertTrue(at(70_000, sure, mode = PlaybackSettings.AUTO)!!.auto)
        assertFalse(at(70_000, sure, mode = button)!!.auto)
        assertFalse(at(70_000, intro, mode = PlaybackSettings.AUTO)!!.auto)
        // Credits always wait for a press.
        assertFalse(at(1_430_000, creditsToEnd.copy(autoSkip = true), mode = PlaybackSettings.AUTO)!!.auto)
    }

    @Test
    fun offShowsNothing() {
        assertNull(at(70_000, intro, mode = PlaybackSettings.OFF))
        assertNull(at(1_430_000, creditsToEnd, mode = PlaybackSettings.OFF))
    }

    @Test
    fun sessionCarriesMarkersAndTheUsersChoice() {
        val body = """
            {"session_id": "s1", "item_id": 5, "version": {"id": 1, "file_name": "a.mkv", "size": 1},
             "stream": {"url": "/x", "protocol": "progressive"},
             "markers": [{"kind": "intro", "start_ms": 1000, "end_ms": 31000, "confidence": 0.9, "source": "detected", "auto_skip": true}],
             "skip_markers": "auto"}
        """.trimIndent()
        val s = ApiJson.decodeFromString(PlaybackSession.serializer(), body)
        assertEquals(1, s.markers.size)
        assertTrue(s.markers[0].autoSkip)
        assertEquals("auto", s.skipMarkers)
        // An older server sends neither: no markers, and the button setting.
        val oldBody = """
            {"session_id": "s1", "item_id": 5, "version": {"id": 1, "file_name": "a.mkv", "size": 1},
             "stream": {"url": "/x", "protocol": "progressive"}}
        """.trimIndent()
        val old = ApiJson.decodeFromString(PlaybackSession.serializer(), oldBody)
        assertTrue(old.markers.isEmpty())
        assertEquals("button", old.skipMarkers)
    }

    @Test
    fun settingsRowMapsToTheServersValue() {
        for (mode in SkipIntroMode.entries) assertEquals(mode, skipIntroMode(mode.wire()))
        assertEquals("button", SkipIntroMode.ShowButton.wire())
        assertEquals("auto", SkipIntroMode.Automatic.wire())
        assertEquals("off", SkipIntroMode.Off.wire())
        // A value from a newer server falls back to the button.
        assertEquals(SkipIntroMode.ShowButton, skipIntroMode("sometimes"))
    }
}
