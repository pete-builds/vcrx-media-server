package io.github.pete_builds.vcrx.tv.ui.profiles

import io.github.pete_builds.vcrx.tv.net.Profile
import io.github.pete_builds.vcrx.tv.net.ProfilesResponse

/**
 * What "Who's watching?" shows. Plain data, so the remote's every press can be
 * checked in unit tests without a TV.
 */
sealed interface PickerStep {
    /** The avatar grid. [focusId] takes focus; [error] is the last failure. */
    data class Grid(val focusId: Long?, val error: String? = null) : PickerStep

    /** The on-screen PIN pad for [profile]. [busy] while the server checks it. */
    data class Pin(val profile: Profile, val digits: String = "", val error: String? = null, val busy: Boolean = false) : PickerStep

    /** A password field, for an admin with no PIN reached from a restricted profile. */
    data class Password(val profile: Profile, val error: String? = null, val busy: Boolean = false) : PickerStep
}

/** What the screen does after a press. */
sealed interface PickerEffect {
    data object None : PickerEffect

    /** Ask the server to switch to [profile] with what was entered. */
    data class Switch(val profile: Profile, val pin: String? = null, val password: String? = null) : PickerEffect

    /** Back from the grid: leave the picker. */
    data object Exit : PickerEffect
}

/** One press: the next step and what to do. */
data class PickerResult(val step: PickerStep, val effect: PickerEffect = PickerEffect.None)

/** The picker's state changes, one function per input. */
object PickerLogic {
    const val MIN_PIN = 4
    const val MAX_PIN = 8

    /**
     * Where focus starts: this device's own profile, else the profile it used
     * last, else the server's suggestion, else the first.
     */
    fun initialFocus(response: ProfilesResponse, lastProfileId: Long?): Long? {
        val ids = response.profiles.map { it.id }
        return listOf(response.currentId, lastProfileId, response.focusId).firstOrNull { it != null && it in ids }
            ?: ids.firstOrNull()
    }

    /** OK on an avatar: switch straight away, or ask for its PIN or password. */
    fun choose(profile: Profile): PickerResult = when (profile.needs) {
        Profile.NEEDS_PIN -> PickerResult(PickerStep.Pin(profile))
        Profile.NEEDS_PASSWORD -> PickerResult(PickerStep.Password(profile))
        else -> PickerResult(PickerStep.Grid(profile.id), PickerEffect.Switch(profile))
    }

    /** A digit from the pad or the remote's number keys. The eighth digit submits. */
    fun digit(step: PickerStep.Pin, d: Char): PickerResult {
        if (step.busy || d !in '0'..'9' || step.digits.length >= MAX_PIN) return PickerResult(step)
        val digits = step.digits + d
        return if (digits.length == MAX_PIN) submit(step.copy(digits = digits)) else PickerResult(step.copy(digits = digits, error = null))
    }

    fun delete(step: PickerStep.Pin): PickerResult =
        if (step.busy) PickerResult(step) else PickerResult(step.copy(digits = step.digits.dropLast(1), error = null))

    /** OK on the pad: send the PIN once it is long enough. */
    fun ok(step: PickerStep.Pin): PickerResult = when {
        step.busy -> PickerResult(step)
        step.digits.length < MIN_PIN -> PickerResult(step.copy(error = "Enter at least $MIN_PIN digits."))
        else -> submit(step)
    }

    private fun submit(step: PickerStep.Pin) =
        PickerResult(step.copy(busy = true, error = null), PickerEffect.Switch(step.profile, pin = step.digits))

    fun password(step: PickerStep.Password, value: String): PickerResult = when {
        step.busy -> PickerResult(step)
        value.isEmpty() -> PickerResult(step.copy(error = "Enter the password."))
        else -> PickerResult(step.copy(busy = true, error = null), PickerEffect.Switch(step.profile, password = value))
    }

    /**
     * The server refused a switch to [profile] with [code]. A wrong PIN clears the
     * pad for another try; a profile that turns out to need a PIN or a password
     * (it changed since the list loaded) gets the right input.
     */
    fun failed(step: PickerStep, profile: Profile, code: String, message: String): PickerStep = when (code) {
        "pin_required" -> PickerStep.Pin(profile)
        "credential_required" -> if (profile.hasPin) PickerStep.Pin(profile) else PickerStep.Password(profile)
        else -> when (step) {
            is PickerStep.Pin -> step.copy(digits = "", busy = false, error = message)
            is PickerStep.Password -> step.copy(busy = false, error = message)
            is PickerStep.Grid -> PickerStep.Grid(profile.id, message)
        }
    }

    /** Back: from the pad or password to the grid, on the same avatar; from the grid, out. */
    fun back(step: PickerStep): PickerResult = when (step) {
        is PickerStep.Pin -> PickerResult(PickerStep.Grid(step.profile.id))
        is PickerStep.Password -> PickerResult(PickerStep.Grid(step.profile.id))
        is PickerStep.Grid -> PickerResult(step, PickerEffect.Exit)
    }

    /** Up to two letters for an avatar with no picture: first and last word, or the first two letters. */
    fun initials(name: String): String {
        val words = name.trim().split(Regex("\\s+")).filter { it.isNotEmpty() }
        val letters = when {
            words.isEmpty() -> "?"
            words.size == 1 -> words[0].take(2)
            else -> "${words.first().first()}${words.last().first()}"
        }
        return letters.uppercase()
    }

    /** "#rrggbb" as an opaque ARGB value, or null when it is not one. */
    fun parseColor(hex: String): Long? {
        if (hex.length != 7 || hex[0] != '#') return null
        return hex.substring(1).toLongOrNull(16)?.let { 0xFF000000L or it }
    }
}

/** Where the app opens: the picker when this device always asks who is watching. */
enum class StartScreen { CONNECT, LOGIN, PICKER, BROWSE }

/**
 * An [invited] device (signed in by an invite or by pairing) is its own
 * principal: it never opens on the picker, whatever "always ask" says.
 */
fun startScreen(hasServer: Boolean, hasToken: Boolean, alwaysAsk: Boolean, invited: Boolean = false): StartScreen = when {
    !hasServer -> StartScreen.CONNECT
    !hasToken -> StartScreen.LOGIN
    alwaysAsk && !invited -> StartScreen.PICKER
    else -> StartScreen.BROWSE
}
