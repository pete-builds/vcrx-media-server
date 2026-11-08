package io.github.pete_builds.vcrx.mobile.data

/**
 * The slots [TokenStore] keeps tokens in, as names. Pure, so the rules are unit
 * tested: a password or automatic sign-in uses the unnamed slot, a household
 * profile "profile:<id>", and an invited device "device".
 */
object TokenSlots {
    const val UNNAMED = ""
    const val DEVICE = "device"
    private const val PROFILE_PREFIX = "profile:"

    fun forProfile(profileId: Long?): String = profileId?.let { "$PROFILE_PREFIX$it" } ?: UNNAMED

    fun profileId(slot: String): Long? = if (slot.startsWith(PROFILE_PREFIX)) slot.removePrefix(PROFILE_PREFIX).toLongOrNull() else null

    fun isInvitedDevice(slot: String): Boolean = slot == DEVICE

    // The unnamed slot keeps the original key names, as on the TV.
    fun ivKey(slot: String) = if (slot.isEmpty()) "iv" else "iv.$slot"

    fun tokenKey(slot: String) = if (slot.isEmpty()) "token" else "token.$slot"
}
