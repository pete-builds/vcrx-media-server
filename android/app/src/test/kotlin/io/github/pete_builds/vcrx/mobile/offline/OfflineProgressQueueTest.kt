package io.github.pete_builds.vcrx.mobile.offline

import io.github.pete_builds.vcrx.mobile.net.ApiException
import java.io.IOException
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OfflineProgressQueueTest {
    private class FakeSink(val fail: Map<Long, ApiException> = emptyMap()) : ProgressSink {
        val played = mutableListOf<Long>()
        val positions = mutableListOf<Pair<Long, Long>>()

        override suspend fun markPlayed(itemId: Long) {
            fail[itemId]?.let { throw it }
            played += itemId
        }

        override suspend fun savePosition(entry: OfflineProgress) {
            fail[entry.itemId]?.let { throw it }
            positions += entry.itemId to entry.positionMs
        }
    }

    private fun p(item: Long, pos: Long, at: Long, played: Boolean = false) =
        OfflineProgress(itemId = item, positionMs = pos, durationMs = 100_000, played = played, recordedAt = at)

    @Test
    fun theNewestEntryPerTitleWins() {
        val q = OfflineProgressQueue()
        q.record(p(1, 10_000, at = 2))
        q.record(p(1, 5_000, at = 1)) // older, arrives late
        q.record(p(1, 20_000, at = 3))
        assertEquals(listOf(p(1, 20_000, 3)), q.entries)
        assertEquals(20_000L, q.resumeFor(1, serverMs = 1_000))
        assertEquals(7_000L, q.resumeFor(2, serverMs = 7_000))
    }

    @Test
    fun syncSendsPositionsAndPlayedInOrderAndEmpties() = runTest {
        val q = OfflineProgressQueue(listOf(p(2, 0, at = 5, played = true), p(1, 42_000, at = 4)))
        val sink = FakeSink()
        assertEquals(2, q.sync(sink))
        assertEquals(listOf(1L to 42_000L), sink.positions)
        assertEquals(listOf(2L), sink.played)
        assertTrue(q.isEmpty)
    }

    @Test
    fun unreachableStopsTheSyncAndKeepsTheRest() = runTest {
        val q = OfflineProgressQueue(listOf(p(1, 1_000, at = 1), p(2, 2_000, at = 2), p(3, 3_000, at = 3)))
        val sink = FakeSink(fail = mapOf(2L to ApiException.Unreachable(IOException("offline"))))
        assertEquals(1, q.sync(sink))
        assertEquals(listOf(2L, 3L), q.entries.map { it.itemId })
    }

    @Test
    fun aTitleTheServerRefusesForGoodIsDropped() = runTest {
        val q = OfflineProgressQueue(listOf(p(1, 1_000, at = 1), p(2, 2_000, at = 2)))
        val sink = FakeSink(fail = mapOf(1L to ApiException.Http(404, "not_found", "gone")))
        assertEquals(1, q.sync(sink))
        assertTrue(q.isEmpty)
        // A signed-out session or a server error keeps the entry for next time.
        assertFalse(OfflineProgressQueue.permanent(ApiException.Unauthorized("sign in")))
        assertFalse(OfflineProgressQueue.permanent(ApiException.Http(503, "busy", "busy")))
        assertFalse(OfflineProgressQueue.permanent(ApiException.Http(429, "slow", "slow")))
    }

    @Test
    fun playedNearTheEnd() {
        assertTrue(OfflineProgressQueue.isPlayed(96_000, 100_000))
        assertFalse(OfflineProgressQueue.isPlayed(90_000, 100_000))
        // Two hours: the last two minutes count, not the last six.
        assertTrue(OfflineProgressQueue.isPlayed(7_100_000, 7_200_000))
        assertFalse(OfflineProgressQueue.isPlayed(6_900_000, 7_200_000))
        assertFalse(OfflineProgressQueue.isPlayed(5_000, null))
    }
}
