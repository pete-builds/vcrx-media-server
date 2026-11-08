package io.github.pete_builds.vcrx.mobile.offline

import io.github.pete_builds.vcrx.mobile.net.Item
import io.github.pete_builds.vcrx.mobile.net.User

/**
 * Who may keep a copy. Accounts and household profiles are the household's own
 * devices and may download; an invited device (GET /me kind "token", or a device
 * that signed in by invite) streams but never downloads, and the server refuses
 * it the whole-file routes anyway. Pure, so each rule is unit tested.
 */
object DownloadEligibility {
    const val KIND_TOKEN = "token"

    /** Whether this session may download at all. [invitedDevice] is what the token store knows locally. */
    fun allowed(me: User?, invitedDevice: Boolean): Boolean {
        if (invitedDevice) return false
        if (me == null) return false
        return me.kind != KIND_TOKEN
    }

    /** Whether [item] gets a Download button: a movie or an episode with a file to fetch. */
    fun canDownload(item: Item, me: User?, invitedDevice: Boolean): Boolean =
        allowed(me, invitedDevice) && item.kind in VIDEO_KINDS && item.versions.isNotEmpty()

    /** The version to keep: the first the server lists, which is the one Play picks by default. */
    fun versionToKeep(item: Item): Long? = item.versions.firstOrNull()?.id

    private val VIDEO_KINDS = setOf("movie", "episode")
}
