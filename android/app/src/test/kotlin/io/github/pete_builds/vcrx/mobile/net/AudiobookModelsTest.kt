package io.github.pete_builds.vcrx.mobile.net

import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Audiobook payloads shaped like server/api/openapi.yaml, nulls and unknown fields included. */
class AudiobookModelsTest {
    private val json = ApiJson

    @Test
    fun bookWithChaptersAndNoProgress() {
        val body = """
            {"id":1,"library_id":4,"title":"The Sample Book","subtitle":null,"author":"A. Author","narrator":"N. Narrator",
             "series":"Sample Series","series_index":1.5,"year":2001,"genre":"Fiction","duration_s":24,"file_count":1,
             "chapter_count":3,"has_cover":true,"added_at":1,"progress":null,"date":"2001","publisher":null,
             "description":"An invented book.","some_future_field":{"x":1},
             "files":[{"id":9,"index":0,"title":"The Sample Book","start_s":0,"duration_s":24,"container":"m4a","codec":"aac","bitrate":64000}],
             "chapters":[{"index":0,"title":"Opening","start_s":0,"end_s":8,"file_id":9},
                         {"index":1,"title":"The Middle","start_s":8,"end_s":16,"file_id":9},
                         {"index":2,"title":"Ending","start_s":16,"end_s":24,"file_id":9}]}
        """.trimIndent()
        val b = json.decodeFromString(AudiobookDetail.serializer(), body)
        assertEquals("A. Author", b.author)
        assertNull(b.progress)
        assertFalse(b.progress.inProgress)
        assertEquals(3, b.chapters.size)
        assertEquals(16.0, b.chapters[2].startS, 0.0)
        assertEquals("Sample Series, book 1.5", b.seriesLine())
        assertEquals(9L, b.files.single().id)
    }

    @Test
    fun queueWithAStreamARefusalAndProgress() {
        val body = """
            {"book_id":2,"title":"First Steps","author":"B. Writer","has_cover":false,"duration_s":24.1,
             "files":[{"id":3,"index":0,"title":"Arrival","start_s":0,"duration_s":12.05,
                       "stream":{"url":"/api/v1/audiobooks/files/3/file","protocol":"progressive","mime_type":"audio/mpeg",
                                 "decision":"direct_play","reason":"the device plays mp3 in mp3"},"refusal":null},
                      {"id":4,"index":1,"title":"Departure","start_s":12.05,"duration_s":12.05,"stream":null,
                       "refusal":{"code":"transcode_unavailable","message":"This book cannot play on this device."}}],
             "chapters":[{"index":0,"title":"Arrival","start_s":0,"end_s":12.05,"file_id":3}],
             "progress":{"position_s":14.5,"finished":false,"updated_at":1793777369369}}
        """.trimIndent()
        val q = json.decodeFromString(AudiobookQueue.serializer(), body)
        assertTrue(q.files[0].stream!!.seekable)
        assertNull(q.files[1].stream)
        assertEquals("transcode_unavailable", q.files[1].refusal?.code)
        assertTrue(q.progress.inProgress)
        assertEquals(14.5, q.progress.positionS, 0.0)
    }

    @Test
    fun progressRequestLeavesOutFinishedUnlessSet() {
        val plain = json.parseToJsonElement(json.encodeToString(AudiobookProgressRequest.serializer(), AudiobookProgressRequest(12.5))).jsonObject
        assertEquals(setOf("position_s"), plain.keys)
        val done = json.encodeToString(AudiobookProgressRequest.serializer(), AudiobookProgressRequest(24.0, finished = true))
        assertEquals("""{"position_s":24.0,"finished":true}""", done)
    }

    @Test
    fun listenAndContinueListening() {
        val listen = json.decodeFromString(
            AudiobookListen.serializer(),
            """{"session_id":"s1","file_id":3,"book_id":2,"decision":"direct_play","reason":"x","stream":{"url":"/a"},"heartbeat_interval_ms":10000}""",
        )
        assertEquals("s1", listen.sessionId)
        assertEquals(2L, listen.bookId)
        val cont = json.decodeFromString(
            AudiobookContinue.serializer(),
            """{"books":[{"id":2,"library_id":4,"title":"First Steps","author":"B. Writer","duration_s":24,"file_count":2,
              "chapter_count":2,"has_cover":false,"added_at":1,"progress":{"position_s":3,"finished":false,"updated_at":5}}]}""",
        )
        assertTrue(cont.books.single().progress.inProgress)
    }
}
