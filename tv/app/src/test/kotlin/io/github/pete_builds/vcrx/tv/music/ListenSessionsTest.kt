package io.github.pete_builds.vcrx.tv.music

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ListenSessionsTest {
    private val tracks = Regex("/api/v1/music/tracks/(\\d+)/")

    @Test
    fun findsTheFileInItsUrl() {
        val s = ListenSessions(tracks) { OpenListen("x", 10_000) }
        assertEquals(42L, s.fileId("http://server:3750/api/v1/music/tracks/42/file"))
        assertEquals(7L, s.fileId("http://server:3750/api/v1/music/tracks/7/stream?codec=aac&start_ms=500"))
        assertNull(s.fileId("http://server:3750/api/v1/audiobooks/files/42/file"))
        assertNull(s.fileId("http://server:3750/api/v1/music/albums/42/cover"))
    }

    @Test
    fun addsTheSessionOnce() {
        assertEquals("/api/v1/music/tracks/1/file?session=abc", ListenSessions.withSession("/api/v1/music/tracks/1/file", "abc"))
        assertEquals(
            "/api/v1/music/tracks/1/stream?codec=aac&session=abc",
            ListenSessions.withSession("/api/v1/music/tracks/1/stream?codec=aac", "abc"),
        )
        val named = "/api/v1/music/tracks/1/file?session=old"
        assertEquals(named, ListenSessions.withSession(named, "abc"))
        assertTrue(ListenSessions.hasSession("/x?a=1&session=s"))
    }

    // The data source opens a file's listen ahead of time; the player's own
    // listen for that file must be the same one, not a second.
    @Test
    fun opensOneListenPerFileUntilForgotten() = runBlocking {
        var opened = 0
        val s = ListenSessions(tracks) { id -> OpenListen("s$id-${++opened}", 10_000) }
        val first = s.get(1)
        assertEquals(first, s.get(1))
        assertEquals(1, opened)
        s.get(2)
        s.forget(1)
        assertEquals("s1-3", s.get(1).sessionId)
        assertEquals(listOf("s1-3", "s2-2"), s.drain().sorted())
        assertEquals(emptyList<String>(), s.drain())
    }
}
