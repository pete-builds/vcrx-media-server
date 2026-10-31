package io.github.pete_builds.vcrx.tv.net

import java.util.concurrent.Executors
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody
import okio.Buffer
import okio.BufferedSource
import okio.ForwardingSource
import okio.buffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Regression test for a crash the emulator smoke test found: response bodies were
 * read wherever the caller resumed, which is Main for every screen, and Android
 * kills the app for network reads on Main. The body here records which thread
 * reads it, and every read must happen on a Dispatchers.IO worker.
 */
class ServerApiThreadTest {
    private class RecordingBody(text: String) : ResponseBody() {
        private val data = Buffer().writeUtf8(text)
        val readers = mutableListOf<String>()
        override fun contentType(): MediaType = "application/json".toMediaType()
        override fun contentLength(): Long = data.size
        override fun source(): BufferedSource = object : ForwardingSource(data) {
            override fun read(sink: Buffer, byteCount: Long): Long {
                readers += Thread.currentThread().name
                return super.read(sink, byteCount)
            }
        }.buffer()
    }

    @Test
    fun bodiesAreReadOnTheIoDispatcher() {
        val body = RecordingBody("""{"rows": [{"id": "continue_watching", "title": "Continue Watching", "items": []}]}""")
        val client = OkHttpClient.Builder()
            .addInterceptor { chain ->
                Response.Builder()
                    .request(chain.request())
                    .protocol(Protocol.HTTP_1_1)
                    .code(200)
                    .message("OK")
                    .body(body)
                    .build()
            }
            .build()
        val api = ServerApi(client, { "http://192.168.1.20:3750" }, ApiJson) { }
        val caller = Executors.newSingleThreadExecutor { Thread(it, "caller-main") }
        val rows = try {
            runBlocking(caller.asCoroutineDispatcher()) { api.home() }
        } finally {
            caller.shutdown()
        }
        assertEquals("continue_watching", rows.single().id)
        assertTrue("body was never read", body.readers.isNotEmpty())
        assertTrue(
            "body read off the IO dispatcher: ${body.readers}",
            body.readers.all { it.startsWith("DefaultDispatcher-worker") },
        )
    }
}
