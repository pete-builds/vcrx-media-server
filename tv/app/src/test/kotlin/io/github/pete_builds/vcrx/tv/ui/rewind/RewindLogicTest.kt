package io.github.pete_builds.vcrx.tv.ui.rewind

import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.httpError
import io.github.pete_builds.vcrx.tv.net.streamRefusal
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

class RewindLogicTest {
    private val ended = """{"error": "loan_ended", "message": "Be kind, rewind. “Harbor Lights” is back on its owner's shelf.",
        "title": "Harbor Lights", "state": "returned"}"""

    @Test
    fun aLoanThatCameBackShowsTheRewindScreen() {
        val state = RewindLogic.from(httpError(401, ended, ApiJson))
        assertNotNull(state)
        // The headline is the screen's title; the line under it names the tape.
        assertEquals("“Harbor Lights” is back on its owner's shelf.", state!!.detail)
        assertEquals("Be kind, rewind.", RewindLogic.HEADLINE)
    }

    @Test
    fun anyOtherSignOutIsNotARewind() {
        // An expired session or a revoked invite still goes to Login.
        assertNull(RewindLogic.from(httpError(401, """{"error": "unauthorized", "message": "Sign in again."}""", ApiJson)))
        assertNull(RewindLogic.from(ApiException.Unauthorized("Wrong PIN.", "wrong_pin")))
        // Someone else's refusal for a lent title is a message on the details screen, not the end of this device.
        assertNull(RewindLogic.from(httpError(409, """{"error": "on_loan", "message": "Out on loan, due back Tue 3 Nov at 18:00."}""", ApiJson)))
        assertNull(RewindLogic.from(httpError(410, """{"error": "loan_ended", "message": "x"}""", ApiJson)))
        assertNull(RewindLogic.from(ApiException.BadResponse("broken")))
    }

    @Test
    fun aRewindWithNoMessageStillSaysWhatHappened() {
        assertEquals(RewindLogic.FALLBACK, RewindLogic.from(ApiException.Unauthorized("Be kind, rewind.", "loan_ended"))!!.detail)
        assertEquals(RewindLogic.FALLBACK, RewindLogic.from(httpError(401, """{"error": "loan_ended"}""", ApiJson))!!.detail)
    }

    @Test
    fun aStreamCutAtTheDueDateSaysRewind() {
        val body = ended.toByteArray()
        assertNotNull(RewindLogic.fromStream(401, body, ApiJson))
        assertNull(RewindLogic.fromStream(401, null, ApiJson))
        assertEquals("Be kind, rewind. “Harbor Lights” is back on its owner's shelf.", streamRefusal(401, body, ApiJson))
        // Control: an admin's stop keeps its own message.
        assertEquals("Playback was stopped on the server: Bedtime.",
            streamRefusal(410, """{"error": "session_terminated", "message": "Bedtime."}""".toByteArray(), ApiJson))
    }
}
