package io.github.pete_builds.vcrx.tv.live

import android.view.KeyEvent
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.LiveChannel
import io.github.pete_builds.vcrx.tv.net.LiveChannelsResponse
import io.github.pete_builds.vcrx.tv.net.LiveProgram
import io.github.pete_builds.vcrx.tv.net.User
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class LiveTvTest {
    private val a = LiveChannel(id = 1, number = "2", name = "News")
    private val b = LiveChannel(id = 2, number = "4.1", name = "Weather")
    private val c = LiveChannel(id = 3, name = "Cooking")

    @Test
    fun upAndDownChangeChannelAndWrapRound() {
        val list = listOf(a, b, c)
        assertEquals(b, LiveTv.step(list, 1, 1))
        assertEquals(a, LiveTv.step(list, 3, 1)) // past the last: the first
        assertEquals(c, LiveTv.step(list, 1, -1)) // before the first: the last
        assertEquals(b, LiveTv.step(list, 99, 1)) // gone from the list: from the top
        assertNull(LiveTv.step(emptyList(), 1, 1))
    }

    @Test
    fun theRemoteKeys() {
        assertEquals(LiveTv.Key.ChannelUp, LiveTv.key(KeyEvent.KEYCODE_DPAD_UP))
        assertEquals(LiveTv.Key.ChannelUp, LiveTv.key(KeyEvent.KEYCODE_CHANNEL_UP))
        assertEquals(LiveTv.Key.ChannelDown, LiveTv.key(KeyEvent.KEYCODE_DPAD_DOWN))
        assertEquals(LiveTv.Key.ChannelDown, LiveTv.key(KeyEvent.KEYCODE_CHANNEL_DOWN))
        assertEquals(LiveTv.Key.Info, LiveTv.key(KeyEvent.KEYCODE_DPAD_CENTER))
        assertEquals(LiveTv.Key.Other, LiveTv.key(KeyEvent.KEYCODE_BACK)) // Back leaves, as everywhere
    }

    @Test
    fun linesAndProgress() {
        val p = LiveProgram(start = 1_000, stop = 3_000, title = "Evening News", episode = "S02E05")
        assertEquals(0.5f, LiveTv.progress(p, 2_000), 0.001f)
        assertEquals(0f, LiveTv.progress(p, 0), 0.001f)
        assertEquals(1f, LiveTv.progress(p, 9_000), 0.001f)
        assertEquals("4.1  Weather", LiveTv.label(b))
        assertEquals("Cooking", LiveTv.label(c))
        assertEquals("Evening News  S02E05", LiveTv.nowLine(a.copy(now = p)))
        assertEquals("No guide", LiveTv.nowLine(a))
        assertEquals("Next  8 PM  Late News", LiveTv.nextLine(a.copy(next = p.copy(title = "Late News"))) { "8 PM" })
        assertEquals("", LiveTv.nextLine(a) { "never" })
    }

    @Test
    fun failuresSayWhatHappened() {
        assertTrue(LiveTv.failure(ApiException.Http(409, "streams_busy", "x")).startsWith("Every live stream is in use"))
        assertEquals("This channel is no longer available.", LiveTv.failure(ApiException.Http(404, "not_found", "x")))
        assertEquals("The server said no.", LiveTv.failure(ApiException.Http(502, "stream_refused", "The server said no.")))
    }

    @Test
    fun theServersLiveAnswersDecode() {
        val body = """{"channels": [
            {"id": 7, "number": "3", "name": "Walk News", "group": null, "has_logo": true,
             "now": {"start": 1, "stop": 2, "title": "Bulletin", "sub_title": null, "description": null, "categories": [], "episode": null},
             "next": {"start": 2, "stop": 3, "title": "Later", "categories": ["News"]}},
            {"id": 8, "name": "Walk Weather", "has_logo": false, "a_field_from_the_future": 1}]}"""
        val list = ApiJson.decodeFromString(LiveChannelsResponse.serializer(), body).channels
        assertEquals(2, list.size)
        assertEquals("Bulletin", list[0].now?.title)
        assertEquals(listOf("News"), list[0].next?.categories)
        assertTrue(list[0].hasLogo)
        assertNull(list[1].number)
        assertTrue(ApiJson.decodeFromString(User.serializer(), """{"id": 1, "username": "pete", "live_tv": true}""").liveTv)
        assertFalse(ApiJson.decodeFromString(User.serializer(), """{"id": 1, "username": "pete"}""").liveTv)
    }
}
