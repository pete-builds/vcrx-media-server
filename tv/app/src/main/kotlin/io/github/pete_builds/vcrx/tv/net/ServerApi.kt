package io.github.pete_builds.vcrx.tv.net

import java.io.IOException
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.Buffer

/** The /api/v1 routes this client uses. Every call throws [ApiException] on failure. */
class ServerApi(
    private val client: OkHttpClient,
    private val baseUrl: () -> String?,
    private val json: Json,
    private val onUnauthorized: () -> Unit,
) {
    /** Checks that [baseUrl] is a VCRX server, before it is saved. Sends no token. */
    suspend fun serverInfo(baseUrl: String): ServerInfo {
        val url = ServerAddress.resolve(baseUrl, "/api/v1/server")
            ?: throw ApiException.BadResponse("That is not a valid address.")
        return send(Request.Builder().url(url).build(), ServerInfo.serializer(), authRoute = false)
    }

    suspend fun login(request: LoginRequest): LoginResponse =
        send(post("/api/v1/auth/login", request, LoginRequest.serializer()), LoginResponse.serializer(), authRoute = false)

    /** Signs in without a password; the server allows it only from its trusted networks. */
    suspend fun autoLogin(request: AutoLoginRequest): LoginResponse =
        send(post("/api/v1/auth/auto", request, AutoLoginRequest.serializer()), LoginResponse.serializer(), authRoute = false)

    suspend fun logout() {
        send<Unit>(Request.Builder().url(url("/api/v1/auth/logout")).post(EMPTY_BODY).build(), null, authRoute = true)
    }

    suspend fun me(): User = get("/api/v1/me", User.serializer())

    suspend fun home(): List<HomeRow> = get("/api/v1/home", HomeResponse.serializer()).rows

    suspend fun libraries(): List<Library> = get("/api/v1/libraries", LibrariesResponse.serializer()).libraries

    suspend fun libraryItems(libraryId: Long, sort: String, order: String, limit: Int, offset: Int): ItemPage {
        val url = url("/api/v1/libraries/$libraryId/items").newBuilder()
            .addQueryParameter("sort", sort)
            .addQueryParameter("order", order)
            .addQueryParameter("limit", limit.toString())
            .addQueryParameter("offset", offset.toString())
            .build()
        return send(Request.Builder().url(url).build(), ItemPage.serializer())
    }

    suspend fun search(query: String, limit: Int = 60): List<Item> {
        val url = url("/api/v1/search").newBuilder()
            .addQueryParameter("q", query)
            .addQueryParameter("limit", limit.toString())
            .build()
        return send(Request.Builder().url(url).build(), ItemsResponse.serializer()).items
    }

    suspend fun item(itemId: Long): Item = get("/api/v1/items/$itemId", Item.serializer())

    /** What is playing now on every device. Admin only, from the LAN. */
    suspend fun nowPlaying(): NowPlaying = get("/api/v1/admin/activity/now", NowPlaying.serializer())

    /** Stops someone's stream; [message] is shown to the viewer. */
    suspend fun terminate(sessionId: String, message: String) {
        send<Unit>(post("/api/v1/admin/activity/sessions/$sessionId/terminate", TerminateRequest(message), TerminateRequest.serializer()), null)
    }

    /** A collection, such as a film series, and its titles in order. */
    suspend fun collection(collectionId: Long): CollectionResponse =
        get("/api/v1/collections/$collectionId", CollectionResponse.serializer())

    suspend fun children(itemId: Long): List<Item> = get("/api/v1/items/$itemId/children", ItemsResponse.serializer()).items

    suspend fun setWatched(itemId: Long, played: Boolean) {
        val body = json.encodeToString(SetWatchedRequest.serializer(), SetWatchedRequest(played)).toRequestBody(JSON)
        send<Unit>(Request.Builder().url(url("/api/v1/items/$itemId/watched")).put(body).build(), null)
    }

    suspend fun startPlayback(request: PlaybackRequest): PlaybackSession =
        send(post("/api/v1/playback/sessions", request, PlaybackRequest.serializer()), PlaybackSession.serializer())

    suspend fun postEvent(sessionId: String, event: PlaybackEvent) {
        send<Unit>(post("/api/v1/playback/sessions/$sessionId/events", event, PlaybackEvent.serializer()), null)
    }

    /** For a converted session: where a stream asked for at [positionMs] starts, and its URL. */
    suspend fun convertedStart(seekPath: String, positionMs: Long): ConvertedStart {
        val url = url(seekPath).newBuilder().setQueryParameter("position_ms", positionMs.coerceAtLeast(0).toString()).build()
        return send(Request.Builder().url(url).build(), ConvertedStart.serializer())
    }

    /**
     * A subtitle file. An embedded track answers 202 while the server reads every text
     * track out of the media file in one pass, which takes minutes on a large file.
     */
    suspend fun subtitle(path: String): SubtitleAnswer {
        val answer = exchange(Request.Builder().url(url(path)).build())
        return when (answer.status) {
            200 -> SubtitleAnswer.Ready(answer.body)
            202 -> SubtitleAnswer.Preparing(answer.retryAfter)
            else -> {
                val error = httpError(answer.status, answer.body.toString(Charsets.UTF_8), json)
                if (error is ApiException.Unauthorized) onUnauthorized()
                throw error
            }
        }
    }

    /**
     * Downloads [bytes] from the speed test route and discards them. Returns the bytes
     * read and the milliseconds from the request to the last byte.
     */
    suspend fun speedTest(bytes: Long): Pair<Long, Long> {
        val request = Request.Builder().url(url("/api/v1/speedtest").newBuilder().setQueryParameter("bytes", bytes.toString()).build()).build()
        return try {
            withContext(Dispatchers.IO) {
                val started = System.nanoTime()
                client.newCall(request).await().use { response ->
                    if (!response.isSuccessful) {
                        val error = httpError(response.code, response.body.string(), json)
                        if (error is ApiException.Unauthorized) onUnauthorized()
                        throw error
                    }
                    val source = response.body.source()
                    val buffer = Buffer()
                    var total = 0L
                    while (true) {
                        val read = source.read(buffer, 64 * 1024)
                        if (read < 0) break
                        total += read
                        buffer.clear()
                    }
                    total to (System.nanoTime() - started) / 1_000_000
                }
            }
        } catch (e: IOException) {
            throw ApiException.Unreachable(e)
        }
    }

    /** Absolute artwork URL; the server answers 404 when there is none and the card draws a placeholder. */
    fun artworkUrl(itemId: Long, kind: String, width: Int): String? =
        baseUrl()?.let { ServerAddress.resolve(it, "/api/v1/items/$itemId/artwork/$kind?width=$width")?.toString() }

    /** Resolves a path from the server, such as stream.url, against the base URL. */
    fun absoluteUrl(path: String): String? =
        baseUrl()?.let { ServerAddress.resolve(it, path)?.toString() }

    internal fun url(path: String): HttpUrl {
        val base = baseUrl() ?: throw ApiException.BadResponse("No server is configured.")
        return ServerAddress.resolve(base, path) ?: throw ApiException.BadResponse("The server address is not valid.")
    }

    internal suspend fun <T> get(path: String, serializer: KSerializer<T>): T =
        send(Request.Builder().url(url(path)).build(), serializer)

    internal fun <B> post(path: String, body: B, serializer: KSerializer<B>): Request =
        Request.Builder().url(url(path)).post(json.encodeToString(serializer, body).toRequestBody(JSON)).build()

    private class Exchange(val status: Int, val body: ByteArray, val retryAfter: Int?)

    /** Runs the call and returns the raw answer, for the routes that are not JSON. */
    private suspend fun exchange(request: Request): Exchange = try {
        withContext(Dispatchers.IO) {
            client.newCall(request).await().use { response ->
                Exchange(response.code, response.body.bytes(), response.header("Retry-After")?.trim()?.toIntOrNull())
            }
        }
    } catch (e: IOException) {
        throw ApiException.Unreachable(e)
    }

    /**
     * Runs the call and decodes the body with [serializer], or ignores it when null.
     * A 401 outside the login routes means the session is gone, so the app returns
     * to the Login screen through [onUnauthorized]. The body is read on the IO
     * dispatcher: callers are often on Main, and a body larger than OkHttp's first
     * buffer is still on the socket.
     */
    internal suspend fun <T> send(request: Request, serializer: KSerializer<T>?, authRoute: Boolean = true): T {
        val (status, body) = try {
            withContext(Dispatchers.IO) {
                client.newCall(request).await().use { response -> response.code to response.body.string() }
            }
        } catch (e: IOException) {
            throw ApiException.Unreachable(e)
        }
        if (status !in 200..299) {
            val error = httpError(status, body, json)
            if (error is ApiException.Unauthorized && authRoute) onUnauthorized()
            throw error
        }
        @Suppress("UNCHECKED_CAST")
        if (serializer == null) return Unit as T
        return try {
            json.decodeFromString(serializer, body)
        } catch (e: SerializationException) {
            throw ApiException.BadResponse("The server sent a response this app cannot read.", e)
        } catch (e: IllegalArgumentException) {
            throw ApiException.BadResponse("The server sent a response this app cannot read.", e)
        }
    }

    private companion object {
        val JSON = "application/json; charset=utf-8".toMediaType()
        val EMPTY_BODY: RequestBody = ByteArray(0).toRequestBody(null)
    }
}

/** One answer from a subtitle URL. */
sealed interface SubtitleAnswer {
    class Ready(val bytes: ByteArray) : SubtitleAnswer

    /** 202: still reading the track out of the file; ask again after [retryAfterSeconds]. */
    data class Preparing(val retryAfterSeconds: Int?) : SubtitleAnswer
}

/** Suspends until the call completes, cancelling it if the coroutine is cancelled. */
private suspend fun Call.await(): Response = suspendCancellableCoroutine { continuation ->
    continuation.invokeOnCancellation { cancel() }
    enqueue(
        object : Callback {
            override fun onResponse(call: Call, response: Response) {
                continuation.resume(response) { _, value, _ -> value.close() }
            }

            override fun onFailure(call: Call, e: IOException) {
                continuation.resumeWithException(e)
            }
        },
    )
}
