package io.github.pete_builds.vcrx.tv.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ApiErrorTest {
    @Test
    fun serverMessageIsShownAsIs() {
        val e = httpError(403, """{"error": "playback_refused", "message": "4K transcodes are off for your account."}""", ApiJson)
        assertTrue(e is ApiException.Http)
        e as ApiException.Http
        assertEquals(403, e.status)
        assertEquals("playback_refused", e.code)
        assertEquals("4K transcodes are off for your account.", e.message)
    }

    @Test
    fun unauthorizedIsItsOwnType() {
        val e = httpError(401, """{"error": "unauthorized", "message": "Session expired."}""", ApiJson)
        assertTrue(e is ApiException.Unauthorized)
        assertEquals("Session expired.", e.message)
    }

    @Test
    fun missingOrBrokenBodiesFallBackToStatusText() {
        val e = httpError(422, "<html>oops</html>", ApiJson) as ApiException.Http
        assertEquals("http_422", e.code)
        assertEquals("The server cannot play this file on this device yet.", e.message)
        assertEquals("The server had an internal error (HTTP 502).", httpError(502, null, ApiJson).message)
    }

    @Test
    fun aStoppedStreamShowsTheAdminsMessage() {
        val body = """{"error": "session_terminated", "message": "Bedtime, finish tomorrow."}""".toByteArray()
        assertEquals("Playback was stopped on the server: Bedtime, finish tomorrow.", streamRefusal(410, body, ApiJson))
        assertEquals("The server refused the stream (HTTP 404).", streamRefusal(404, null, ApiJson))
    }

    @Test
    fun aFullServerSaysSoPlainly() {
        val body = """{"error": "busy", "message": "The server is converting as many streams as it allows. Try again shortly."}""".toByteArray()
        assertEquals(BUSY_MESSAGE, streamRefusal(503, body, ApiJson))
        assertTrue(isBusy(httpError(503, body.toString(Charsets.UTF_8), ApiJson)))
        assertFalse(isBusy(httpError(503, """{"error": "not_ready", "message": "Starting."}""", ApiJson)))
    }
}
