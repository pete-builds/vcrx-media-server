package io.github.pete_builds.vcrx.tv.net

import java.io.IOException
import kotlinx.serialization.json.Json

/** Every failure the UI distinguishes. Screens map these to their error states. */
sealed class ApiException(message: String, cause: Throwable? = null) : Exception(message, cause) {
    /** No HTTP answer at all: refused, timed out, no route, DNS failure. */
    class Unreachable(cause: IOException) :
        ApiException("Cannot reach the server (${cause.javaClass.simpleName}: ${cause.message ?: "no detail"}).", cause)

    /**
     * 401: the token is missing, expired or revoked, or (from a profile switch)
     * the PIN or password was missing or wrong; [code] says which.
     */
    class Unauthorized(message: String, val code: String = "unauthorized") : ApiException(message)

    /** Any other non-success status, with the server's machine code and TV-safe text. */
    class Http(val status: Int, val code: String, message: String) : ApiException(message)

    /** The server answered with something this client cannot read. */
    class BadResponse(message: String, cause: Throwable? = null) : ApiException(message, cause)
}

/** Builds the exception for a non-2xx response from its status and raw body. */
fun httpError(status: Int, body: String?, json: Json): ApiException {
    val parsed = body
        ?.takeIf { it.isNotBlank() }
        ?.let { runCatching { json.decodeFromString(ErrorBody.serializer(), it) }.getOrNull() }
    val message = parsed?.message?.takeIf { it.isNotBlank() } ?: defaultMessage(status)
    val code = parsed?.error?.takeIf { it.isNotBlank() } ?: "http_$status"
    return if (status == 401) ApiException.Unauthorized(message, code) else ApiException.Http(status, code, message)
}

private fun defaultMessage(status: Int): String = when (status) {
    401 -> "Please sign in again."
    403 -> "You do not have access to this."
    404 -> "Not found on the server."
    409 -> "The server refused this because of a conflict."
    422 -> "The server cannot play this file on this device yet."
    429 -> "Too many attempts. Wait a minute and try again."
    in 500..599 -> "The server had an internal error (HTTP $status)."
    else -> "The server answered HTTP $status."
}

/**
 * What to tell the viewer when the server refuses a stream mid-play. A 410 means
 * an admin stopped it from Activity, and the body carries their message. A 503
 * busy means every converted-stream slot on the server is taken.
 */
fun streamRefusal(status: Int, body: ByteArray?, json: Json): String {
    val error = httpError(status, body?.toString(Charsets.UTF_8), json)
    return when {
        error is ApiException.Http && error.code == "session_terminated" -> "Playback was stopped on the server: ${error.message}"
        error is ApiException.Http && error.code == BUSY -> BUSY_MESSAGE
        else -> "The server refused the stream (HTTP $status)."
    }
}

/** True for the server's 503 busy answer: it is converting as many streams as it allows. */
fun isBusy(error: ApiException): Boolean = error is ApiException.Http && error.status == 503 && error.code == BUSY

/** The error code of a 503 from a converted stream when every slot is taken. */
const val BUSY = "busy"

/** Shown for [BUSY], on the TV's own terms: nothing is wrong, the server is full. */
const val BUSY_MESSAGE =
    "The server is converting as many streams as it allows right now. Try again in a minute, or stop playback on another screen."
