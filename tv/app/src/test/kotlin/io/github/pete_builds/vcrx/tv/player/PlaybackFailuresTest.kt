package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.BUSY_MESSAGE
import io.github.pete_builds.vcrx.tv.net.httpError
import java.io.IOException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** What the viewer sees when starting, seeking or streaming fails. */
class PlaybackFailuresTest {
    private fun http(status: Int, code: String, message: String) =
        httpError(status, """{"error": "$code", "message": "$message"}""", ApiJson)

    @Test
    fun policyAndUnplayableFilesAreRefusals() {
        assertEquals(Phase.Refused("Not for your account."), phaseFor(http(403, "playback_refused", "Not for your account.")))
        assertEquals(Phase.Refused("Needs video conversion."), phaseFor(http(422, "transcode_unavailable", "Needs video conversion.")))
    }

    @Test
    fun busyIsItsOwnFailureWithRetry() {
        val busy = phaseFor(http(503, "busy", "The server is converting as many streams as it allows. Try again shortly.")) as Phase.Failed
        assertTrue(busy.busy)
        assertFalse(busy.unreachable)
        assertEquals(BUSY_MESSAGE, busy.message)
        // Any other 503, such as a proxy in front of the server, is not "busy".
        val other = phaseFor(httpError(503, null, ApiJson)) as Phase.Failed
        assertFalse(other.busy)
    }

    @Test
    fun aSeekOnAStoppedSessionShowsTheAdminsMessage() {
        val failed = phaseFor(http(410, "session_terminated", "Bedtime.")) as Phase.Failed
        assertEquals("Playback was stopped on the server: Bedtime.", failed.message)
        assertFalse(failed.busy)
    }

    @Test
    fun noAnswerIsUnreachable() {
        val failed = phaseFor(ApiException.Unreachable(IOException("timeout"))) as Phase.Failed
        assertTrue(failed.unreachable)
        assertEquals(
            Phase.Failed("That playback session has already ended.", false),
            phaseFor(http(409, "session_ended", "That playback session has already ended.")),
        )
    }

    @Test
    fun streamErrorsGoThroughStreamRefusal() {
        val busy = streamFailure(503, """{"error": "busy", "message": "Full."}""".toByteArray(), ApiJson)
        assertTrue(busy.busy)
        assertEquals(BUSY_MESSAGE, busy.message)
        val stopped = streamFailure(410, """{"error": "session_terminated", "message": "Bedtime."}""".toByteArray(), ApiJson)
        assertFalse(stopped.busy)
        assertEquals("Playback was stopped on the server: Bedtime.", stopped.message)
        val other = streamFailure(503, null, ApiJson)
        assertFalse(other.busy)
        assertEquals("The server refused the stream (HTTP 503).", other.message)
    }
}
