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

    /**
     * Whether a book gets a Download button: the whole-file route, which the
     * server refuses an invited device (it plays a book only inside its own
     * listen, streamed), and a book with files to fetch.
     */
    fun canDownloadBook(fileCount: Int, me: User?, invitedDevice: Boolean): Boolean = allowed(me, invitedDevice) && fileCount > 0

    /**
     * Whether an episode gets a Download button. Podcasts are accounts only, so an
     * invited device never sees one; the episode needs a stream to fetch.
     */
    fun canDownloadEpisode(streamUrl: String, me: User?, invitedDevice: Boolean): Boolean =
        allowed(me, invitedDevice) && me?.podcasts == true && streamUrl.isNotEmpty()

    /** The version to keep: the first the server lists, which is the one Play picks by default. */
    fun versionToKeep(item: Item): Long? = item.versions.firstOrNull()?.id

    private val VIDEO_KINDS = setOf("movie", "episode")
}
