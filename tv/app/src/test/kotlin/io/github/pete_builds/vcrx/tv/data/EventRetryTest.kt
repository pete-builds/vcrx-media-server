package io.github.pete_builds.vcrx.tv.data

import io.github.pete_builds.vcrx.tv.net.ApiException
import java.io.IOException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** A stop event is kept and sent again only when the failure may pass. */
class EventRetryTest {
    @Test
    fun theServerSayingNoIsFinal() {
        assertTrue(EventRetry.permanent(ApiException.Unauthorized("signed out")))
        for (status in listOf(403, 404, 409, 410)) {
            assertTrue("$status", EventRetry.permanent(ApiException.Http(status, "x", "x")))
        }
    }

    @Test
    fun aBodyTheServerCouldNotReadIsKept() {
        // A 400 is the server rejecting the body (a field it does not know yet), not the
        // stop itself; dropping it would lose the resume position.
        assertFalse(EventRetry.permanent(ApiException.Http(400, "bad_request", "Invalid JSON body")))
    }

    @Test
    fun noNetworkOrAServerErrorIsTriedAgain() {
        assertFalse(EventRetry.permanent(ApiException.Unreachable(IOException("no route"))))
        assertFalse(EventRetry.permanent(ApiException.Http(503, "x", "restarting")))
        assertFalse(EventRetry.permanent(ApiException.Http(500, "x", "x")))
        assertFalse(EventRetry.permanent(ApiException.BadResponse("cut off")))
    }

    @Test
    fun backoffDoublesToTenMinutes() {
        assertEquals(15_000L, EventRetry.backoffMs(0))
        assertEquals(30_000L, EventRetry.backoffMs(1))
        assertEquals(480_000L, EventRetry.backoffMs(5))
        assertEquals(600_000L, EventRetry.backoffMs(6))
        assertEquals(600_000L, EventRetry.backoffMs(1_000))
    }
}
