package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.net.User
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ListenSessionsTest {
    private val base = "http://192.168.1.20:3750"

    // What Media3 opens for a queue: a downloaded-style file URL, a direct file,
    // and a converted stream with its own query.
    private val queue = listOf(
        "$base/api/v1/music/tracks/11/file",
        "$base/api/v1/music/tracks/12/file",
        "$base/api/v1/music/tracks/13/stream?codec=aac&start_ms=0",
    )

    private fun sessions(opened: MutableList<Long>) = ListenSessions(ListenSessions.TRACKS) { id ->
        opened += id
        OpenListen("listen-$id", 10_000)
    }

    @Test
    fun anInvitedDeviceAsksForEveryTrackInsideItsOwnListen() {
        val opened = mutableListOf<Long>()
        val s = sessions(opened).apply { enabled = true }
        val resolved = queue.map { s.resolve(it) }
        assertEquals(
            listOf(
                "$base/api/v1/music/tracks/11/file?session=listen-11",
                "$base/api/v1/music/tracks/12/file?session=listen-12",
                "$base/api/v1/music/tracks/13/stream?codec=aac&start_ms=0&session=listen-13",
            ),
            resolved,
        )
        assertEquals(listOf(11L, 12L, 13L), opened)
        // Media3 opens a track again to seek or retry: the same listen, not a new one.
        assertEquals("$base/api/v1/music/tracks/12/file?session=listen-12", s.resolve(queue[1]))
        assertEquals(listOf(11L, 12L, 13L), opened)
        // Covers and anything else are not tracks.
        assertEquals("$base/api/v1/music/albums/4/cover?width=480", s.resolve("$base/api/v1/music/albums/4/cover?width=480"))
    }

    @Test
    fun theHouseholdIsUnchanged() {
        val opened = mutableListOf<Long>()
        val s = sessions(opened)
        assertEquals(queue, queue.map { s.resolve(it) })
        assertEquals(emptyList<Long>(), opened)
    }

    @Test
    fun streamOnlyIsTheInvitedDevice() {
        assertTrue(ListenSessions.streamOnly(User(id = 1, username = "a", kind = "token"), invitedDevice = false))
        assertTrue(ListenSessions.streamOnly(null, invitedDevice = true))
        assertFalse(ListenSessions.streamOnly(User(id = 1, username = "a", kind = "user"), invitedDevice = false))
        assertFalse(ListenSessions.streamOnly(User(id = 1, username = "a", kind = "profile"), invitedDevice = false))
        assertFalse(ListenSessions.streamOnly(null, invitedDevice = false))
    }

    @Test
    fun findsTheTrackInItsUrl() {
        val s = sessions(mutableListOf())
        assertEquals(42L, s.fileId("$base/api/v1/music/tracks/42/file"))
        assertEquals(7L, s.fileId("$base/api/v1/music/tracks/7/stream?codec=aac"))
        assertNull(s.fileId("$base/api/v1/music/tracks/42/sessions"))
        assertNull(s.fileId("$base/api/v1/versions/42/file"))
    }

    @Test
    fun addsTheSessionOnce() {
        assertEquals("/api/v1/music/tracks/1/file?session=abc", ListenSessions.withSession("/api/v1/music/tracks/1/file", "abc"))
        val named = "/api/v1/music/tracks/1/file?session=old"
        assertEquals(named, ListenSessions.withSession(named, "abc"))
        assertEquals("/x?session=a%2Fb", ListenSessions.withSession("/x", "a/b"))
    }

    // The data source opens a track's listen ahead of time; the player's own
    // listen for that track must be the same one, and a new queue closes the rest.
    @Test
    fun opensOneListenPerTrackUntilForgotten() = runBlocking {
        val opened = mutableListOf<Long>()
        val s = sessions(opened).apply { enabled = true }
        s.resolve(queue[0])
        assertEquals("listen-11", s.get(11).sessionId)
        assertEquals(listOf(11L), opened)
        s.get(12)
        s.forget(11)
        s.get(11)
        assertEquals(listOf(11L, 12L, 11L), opened)
        assertEquals(listOf("listen-11", "listen-12"), s.drain().sorted())
        assertEquals(emptyList<String>(), s.drain())
    }
}
