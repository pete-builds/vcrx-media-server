package io.github.pete_builds.vcrx.tv.ui.rewind

import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.LOAN_ENDED
import io.github.pete_builds.vcrx.tv.net.httpError
import kotlinx.serialization.json.Json

/**
 * A lent tape (the owner's printed card, redeemed on this TV by typing its
 * code) has come back: returned early or run out. The server answers this
 * device's next request with 401 loan_ended, and the TV says "Be kind, rewind."
 * instead of asking to sign in, since there is nothing to sign in to.
 */
data class RewindState(
    /** The line under the headline: which tape went back. */
    val detail: String,
)

/** Decides when the TV shows [RewindScreen], as plain data so it is unit tested. */
object RewindLogic {
    const val HEADLINE = "Be kind, rewind."

    /** Said when the server sends no title. */
    const val FALLBACK = "This tape is back on its owner's shelf."

    /** The rewind screen for [error], or null for any other failure. */
    fun from(error: ApiException): RewindState? {
        if (error !is ApiException.Unauthorized || error.code != LOAN_ENDED) return null
        // The server's line starts with the headline. Anything else (a body
        // that did not parse falls back to "Please sign in again.") is not
        // worth showing under it.
        val message = error.message.orEmpty()
        val detail = if (message.startsWith(HEADLINE)) message.removePrefix(HEADLINE).trim() else ""
        return RewindState(detail.ifEmpty { FALLBACK })
    }

    /** The same for a refused stream mid-play: its HTTP status and body. */
    fun fromStream(status: Int, body: ByteArray?, json: Json): RewindState? =
        from(httpError(status, body?.toString(Charsets.UTF_8), json))
}
