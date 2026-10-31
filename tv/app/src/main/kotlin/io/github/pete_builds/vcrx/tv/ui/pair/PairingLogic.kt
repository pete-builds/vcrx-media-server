package io.github.pete_builds.vcrx.tv.ui.pair

import io.github.pete_builds.vcrx.tv.net.PairPollResponse
import io.github.pete_builds.vcrx.tv.net.PairStartResponse

/**
 * Pairing with a phone, as plain data so every step can be checked in unit tests
 * without a TV. The TV asks the server for a code, shows it with a QR code, and
 * polls until an admin's phone approves it (the device-code flow, entirely on the
 * local network). The server enforces every rule; this decides what the screen shows.
 */
sealed interface PairingState {
    /** Asking the server for a code. */
    data object Starting : PairingState

    /** Showing [userCode] and the QR code, polling every [intervalMs] until [expiresAtMs]. */
    data class Showing(
        val userCode: String,
        val url: String,
        val qrPng: String,
        val deviceCode: String,
        val expiresAtMs: Long,
        val intervalMs: Long,
        /** A passing problem, such as the server not answering one poll. */
        val note: String? = null,
    ) : PairingState

    /** Approved: sign in with [token] as the invited device [name]. */
    data class Approved(val token: String, val name: String) : PairingState

    /** The code ran out or was turned away; a new one is a button press away. */
    data class Expired(val message: String) : PairingState

    /** Something the TV cannot fix by waiting, such as too many codes from this address. */
    data class Failed(val message: String) : PairingState
}

/** What one poll came back with. */
sealed interface PollOutcome {
    data class Answer(val response: PairPollResponse) : PollOutcome

    /** The server no longer knows the code: expired, turned away or already used. */
    data object Gone : PollOutcome

    /** Too many polls from this address (429): wait longer. */
    data object SlowDown : PollOutcome

    /** No answer at all; the network may come back before the code runs out. */
    data class Unreachable(val message: String) : PollOutcome

    data class Error(val message: String) : PollOutcome
}

object PairingLogic {
    /** Never poll faster than this, whatever the server says. */
    const val MIN_INTERVAL_MS = 2_000L
    const val MAX_INTERVAL_MS = 60_000L

    const val EXPIRED = "The code ran out before a phone approved it."
    const val GONE = "The code was turned away or ran out."

    fun started(response: PairStartResponse, nowMs: Long): PairingState.Showing = PairingState.Showing(
        userCode = response.userCode,
        url = response.verificationUrl,
        qrPng = response.qrPng,
        deviceCode = response.deviceCode,
        expiresAtMs = nowMs + response.expiresIn.coerceAtLeast(0) * 1000L,
        intervalMs = interval(response.interval),
    )

    private fun interval(seconds: Int): Long = (seconds * 1000L).coerceIn(MIN_INTERVAL_MS, MAX_INTERVAL_MS)

    /** Whether the code has run out by [nowMs], so polling stops. */
    fun isExpired(state: PairingState.Showing, nowMs: Long): Boolean = nowMs >= state.expiresAtMs

    /** The state after a poll that came back at [nowMs]. */
    fun afterPoll(state: PairingState.Showing, outcome: PollOutcome, nowMs: Long): PairingState {
        val approved = (outcome as? PollOutcome.Answer)?.response?.takeIf { it.status == "approved" }
        if (approved != null) {
            val token = approved.token
            if (token.isNullOrBlank()) return PairingState.Failed("The server approved this TV but sent no session. Try again.")
            val name = approved.user?.displayName ?: approved.user?.username ?: "This TV"
            return PairingState.Approved(token, name)
        }
        if (isExpired(state, nowMs)) return PairingState.Expired(EXPIRED)
        return when (outcome) {
            is PollOutcome.Answer -> state.copy(intervalMs = interval(outcome.response.interval), note = null)
            PollOutcome.Gone -> PairingState.Expired(GONE)
            PollOutcome.SlowDown -> state.copy(intervalMs = (state.intervalMs * 2).coerceAtMost(MAX_INTERVAL_MS))
            is PollOutcome.Unreachable -> state.copy(note = outcome.message)
            is PollOutcome.Error -> PairingState.Failed(outcome.message)
        }
    }

    /** Seconds left on the code, for the countdown. */
    fun secondsLeft(state: PairingState.Showing, nowMs: Long): Long = ((state.expiresAtMs - nowMs + 999) / 1000).coerceAtLeast(0)

    private val INVITE = Regex("^[a-z2-7]{32}$")

    /**
     * An invite typed with the remote, or pasted as its whole link: the 32
     * characters after "#/invite/", any case, spaces and dashes ignored. Null
     * when it cannot be an invite, so the server is not asked.
     */
    fun normalizeInvite(text: String): String? {
        val tail = text.trim().substringAfterLast("/invite/").substringAfterLast('/')
        val clean = tail.lowercase().filter { it != ' ' && it != '-' }
        return clean.takeIf { INVITE.matches(it) }
    }
}
