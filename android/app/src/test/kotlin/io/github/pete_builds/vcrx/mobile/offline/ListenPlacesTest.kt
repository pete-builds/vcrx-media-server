package io.github.pete_builds.vcrx.mobile.offline

import io.github.pete_builds.vcrx.mobile.net.ApiException
import java.io.IOException
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ListenPlacesTest {
    private class FakeSink(val fail: Map<String, ApiException> = emptyMap()) : ListenSink {
        val sent = mutableListOf<ListenPlace>()

        override suspend fun saveBook(place: ListenPlace) = save(place)

        override suspend fun saveEpisode(place: ListenPlace) = save(place)

        private fun save(place: ListenPlace) {
            fail[place.key]?.let { throw it }
            sent += place
        }
    }

    private fun book(id: Long, pos: Double, at: Long, finished: Boolean = false) = ListenPlace(ListenPlace.BOOK, id, pos, finished = finished, recordedAt = at)
    private fun episode(id: Long, pos: Double, at: Long, finished: Boolean = false) = ListenPlace(ListenPlace.EPISODE, id, pos, 600.0, finished, at)

    @Test
    fun aPlaceTheServerTookIsNotQueued() {
        val p = ListenPlaces()
        p.record(book(1, 30.0, 1), sent = true)
        assertTrue(p.pending.isEmpty())
        // Online, the server's place wins; with no answer, the phone's last one.
        assertEquals(50.0, p.resumeFor(ListenPlace.BOOK, 1, 50.0), 0.0)
        assertEquals(30.0, p.resumeFor(ListenPlace.BOOK, 1, null), 0.0)
        assertEquals(0.0, p.resumeFor(ListenPlace.BOOK, 2, null), 0.0)
    }

    @Test
    fun anUnsentPlaceWinsUntilItIsSent() {
        val p = ListenPlaces()
        p.record(book(1, 30.0, 1), sent = true)
        p.record(book(1, 90.0, 2), sent = false)
        assertEquals(90.0, p.resumeFor(ListenPlace.BOOK, 1, 30.0), 0.0)
        // An older save answered late does not clear the newer pending one.
        p.record(book(1, 31.0, 1), sent = true)
        assertEquals(1, p.pending.size)
        assertEquals(90.0, p.resumeFor(ListenPlace.BOOK, 1, null), 0.0)
        // A newer one taken by the server does.
        p.record(book(1, 95.0, 3), sent = true)
        assertTrue(p.pending.isEmpty())
    }

    @Test
    fun finishedStartsOver() {
        val p = ListenPlaces()
        p.record(episode(4, 590.0, 1, finished = true), sent = false)
        assertEquals(0.0, p.resumeFor(ListenPlace.EPISODE, 4, 120.0), 0.0)
        val q = ListenPlaces()
        assertEquals(0.0, q.resumeFor(ListenPlace.EPISODE, 4, 120.0, serverFinished = true), 0.0)
        // A book and an episode with the same id are different things.
        assertNull(p.last(ListenPlace.BOOK, 4))
    }

    @Test
    fun syncSendsOldestFirstAndStopsWhenTheServerIsAway() = runTest {
        val p = ListenPlaces(pending = listOf(book(1, 10.0, 5), episode(2, 20.0, 1), book(3, 30.0, 9)))
        val sink = FakeSink(fail = mapOf("book:1" to ApiException.Unreachable(IOException("down"))))
        assertEquals(1, p.sync(sink))
        assertEquals(listOf("episode:2"), sink.sent.map { it.key })
        assertEquals(setOf("book:1", "book:3"), p.pending.map { it.key }.toSet())
    }

    @Test
    fun aPermanentRefusalLeavesTheQueue() = runTest {
        val p = ListenPlaces(pending = listOf(book(1, 10.0, 1), book(2, 20.0, 2)))
        val sink = FakeSink(fail = mapOf("book:1" to ApiException.Http(404, "not_found", "gone")))
        assertEquals(1, p.sync(sink))
        assertTrue(p.pending.isEmpty())
        // A 401 is not permanent: sign in again and it still goes.
        val q = ListenPlaces(pending = listOf(book(1, 10.0, 1)))
        q.sync(FakeSink(fail = mapOf("book:1" to ApiException.Unauthorized("sign in"))))
        assertEquals(1, q.pending.size)
    }

    @Test
    fun theNewestPlaceWins() {
        val p = ListenPlaces(latest = listOf(book(1, 50.0, 10), book(1, 20.0, 5)))
        assertEquals(50.0, p.last(ListenPlace.BOOK, 1)!!.positionS, 0.0)
        p.forget(ListenPlace.BOOK, 1)
        assertNull(p.last(ListenPlace.BOOK, 1))
    }
}
