package io.github.pete_builds.vcrx.tv.net

import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A request still in flight when the viewer switches profile carries the old
 * session's token, and the server ended that session with the switch. Its 401
 * must not sign the new profile out.
 */
class StaleUnauthorizedTest {
    /** A client that sends [sent] as the bearer token, as AuthInterceptor would, and is answered 401. */
    private fun client(sent: String?) = OkHttpClient.Builder()
        .addInterceptor { chain ->
            val request = chain.request().newBuilder()
            if (sent != null) request.header("Authorization", "Bearer $sent")
            chain.proceed(request.build())
        }
        .addInterceptor { chain ->
            Response.Builder()
                .request(chain.request())
                .protocol(Protocol.HTTP_1_1)
                .code(401)
                .message("Unauthorized")
                .body("""{"error": "unauthorized", "message": "Session ended."}""".toResponseBody("application/json".toMediaType()))
                .build()
        }
        .build()

    /** How many times a 401 for a request sent with [sent] signs out while the app holds [current]. */
    private fun signOuts(sent: String?, current: String?): Int {
        var calls = 0
        val api = ServerApi(client(sent), { "http://192.168.1.20:3750" }, ApiJson, { current }) { calls++ }
        val thrown = runCatching { runBlocking { api.home() } }.exceptionOrNull()
        assertTrue("a 401 still throws Unauthorized to the caller", thrown is ApiException.Unauthorized)
        return calls
    }

    @Test
    fun a401ForTheCurrentTokenSignsOut() {
        assertEquals(1, signOuts(sent = "token-a", current = "token-a"))
    }

    @Test
    fun a401ForATokenReplacedSinceTheRequestLeftChangesNothing() {
        assertEquals(0, signOuts(sent = "token-a", current = "token-b"))
    }

    @Test
    fun a401ForARequestSentSignedOutSignsOutOnlyWhileStillSignedOut() {
        assertEquals(1, signOuts(sent = null, current = null))
        assertEquals(0, signOuts(sent = null, current = "token-b"))
    }

    @Test
    fun theHeaderMustCarryExactlyTheCurrentToken() {
        assertTrue(SessionToken.answersCurrent("Bearer token-a", "token-a"))
        assertFalse(SessionToken.answersCurrent("Bearer token-a", "token-b"))
        assertFalse(SessionToken.answersCurrent("Bearer token-a", null))
        assertFalse(SessionToken.answersCurrent(null, "token-a"))
        assertTrue(SessionToken.answersCurrent(null, null))
    }
}
