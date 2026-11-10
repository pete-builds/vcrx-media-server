package io.github.pete_builds.vcrx.mobile.net

import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

// The audiobook routes, as extensions so the audiobook screens live in their
// own files. Same transport, errors and 401 handling as every other call.
// Ported from the TV's net/AudiobookApi.kt.

suspend fun ServerApi.audiobooks(libraryId: Long, sort: String = "title", limit: Int = 500): AudiobookPage =
    get("/api/v1/audiobooks/libraries/$libraryId/books?sort=$sort&limit=$limit", AudiobookPage.serializer())

/** Books in progress; empty from a server without audiobooks, which answers 404. */
suspend fun ServerApi.audiobooksInProgress(): List<AudiobookBook> = try {
    get("/api/v1/audiobooks/continue", AudiobookContinue.serializer()).books
} catch (e: ApiException.Http) {
    if (e.status == 404) emptyList() else throw e
}

suspend fun ServerApi.audiobook(bookId: Long): AudiobookDetail =
    get("/api/v1/audiobooks/books/$bookId", AudiobookDetail.serializer())

suspend fun ServerApi.audiobookQueue(bookId: Long, capabilities: Capabilities): AudiobookQueue =
    send(post("/api/v1/audiobooks/books/$bookId/queue", AudiobookQueueRequest(capabilities), AudiobookQueueRequest.serializer()), AudiobookQueue.serializer())

suspend fun ServerApi.audiobookListen(fileId: Long, capabilities: Capabilities): AudiobookListen =
    send(post("/api/v1/audiobooks/files/$fileId/sessions", AudiobookListenRequest(capabilities), AudiobookListenRequest.serializer()), AudiobookListen.serializer())

suspend fun ServerApi.saveAudiobookProgress(bookId: Long, positionS: Double, finished: Boolean? = null): AudiobookProgress =
    send(put("/api/v1/audiobooks/books/$bookId/progress", AudiobookProgressRequest(positionS, finished), AudiobookProgressRequest.serializer()), AudiobookProgress.serializer())

/** Marks a book finished, or not; either way the next listen starts from the beginning. */
suspend fun ServerApi.setAudiobookFinished(bookId: Long, finished: Boolean): AudiobookProgress {
    val b = Request.Builder().url(url("/api/v1/audiobooks/books/$bookId/finished"))
    val request = if (finished) b.post(ByteArray(0).toRequestBody(null)).build() else b.delete().build()
    return send(request, AudiobookProgress.serializer())
}

fun ServerApi.audiobookCoverUrl(bookId: Long, width: Int): String? = absoluteUrl("/api/v1/audiobooks/books/$bookId/cover?width=$width")
