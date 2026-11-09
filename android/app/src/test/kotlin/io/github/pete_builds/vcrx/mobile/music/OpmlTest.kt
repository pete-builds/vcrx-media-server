package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.net.OpmlFailure
import io.github.pete_builds.vcrx.mobile.net.OpmlImportResult
import io.github.pete_builds.vcrx.mobile.net.User
import java.io.ByteArrayInputStream
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OpmlTest {
    private val sample = """<?xml version="1.0" encoding="UTF-8"?>
        <opml version="2.0"><head><title>Subscriptions</title></head><body>
        <outline type="rss" text="Harbor Talk" xmlUrl="https://example.com/feed.xml"/></body></opml>""".toByteArray()

    @Test
    fun anOpmlFileIsSentAsItIs() {
        assertNull(Opml.check(sample))
        // A byte order mark and capitals are fine.
        assertNull(Opml.check(byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte()) + "<OPML version=\"1.0\"></OPML>".toByteArray()))
        assertEquals("text/x-opml", Opml.MEDIA_TYPE)
    }

    @Test
    fun theWrongFileIsRefusedBeforeSending() {
        assertEquals("That file is empty.", Opml.check(ByteArray(0)))
        assertTrue(Opml.check("<html><body>not a feed list</body></html>".toByteArray())!!.startsWith("That is not an OPML file"))
        assertTrue(Opml.check(ByteArray(Opml.MAX_BYTES + 1) { 'x'.code.toByte() })!!.startsWith("That file is over 1 MB"))
    }

    @Test
    fun readingStopsJustPastTheLimit() {
        val big = ByteArray(Opml.MAX_BYTES * 3)
        assertEquals(Opml.MAX_BYTES + 1, Opml.read(ByteArrayInputStream(big)).size)
        assertEquals(sample.toList(), Opml.read(ByteArrayInputStream(sample)).toList())
    }

    @Test
    fun theOutcomeInWords() {
        assertEquals("Added 3 shows.", Opml.summary(OpmlImportResult(added = 3)))
        assertEquals(
            "Added 1 show. 2 were already in your podcasts. 1 could not be added.",
            Opml.summary(OpmlImportResult(added = 1, already = 2, failed = listOf(OpmlFailure("https://example.org/x", "not RSS")))),
        )
    }

    @Test
    fun accountsOnly() {
        assertTrue(Opml.allowed(User(id = 1, username = "a", kind = "user", podcasts = true), invitedDevice = false))
        assertTrue(Opml.allowed(User(id = 1, username = "a", kind = "profile", podcasts = true), invitedDevice = false))
        assertFalse(Opml.allowed(User(id = 1, username = "a", kind = "token", podcasts = true), invitedDevice = false))
        assertFalse(Opml.allowed(User(id = 1, username = "a", kind = "user", podcasts = true), invitedDevice = true))
        assertFalse(Opml.allowed(User(id = 1, username = "a", kind = "user", podcasts = false), invitedDevice = false))
        assertFalse(Opml.allowed(null, invitedDevice = false))
    }
}
