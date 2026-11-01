package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** A guide entry. Times are Unix milliseconds. */
@Serializable
data class LiveProgram(
    val start: Long,
    val stop: Long,
    val title: String,
    @SerialName("sub_title") val subTitle: String? = null,
    val description: String? = null,
    val categories: List<String> = emptyList(),
    val episode: String? = null,
)

/** A Live TV channel as GET /live/channels lists it: never its stream URL. */
@Serializable
data class LiveChannel(
    val id: Long,
    val number: String? = null,
    val name: String,
    val group: String? = null,
    @SerialName("has_logo") val hasLogo: Boolean = false,
    val now: LiveProgram? = null,
    val next: LiveProgram? = null,
)

@Serializable
data class LiveChannelsResponse(val channels: List<LiveChannel> = emptyList())

/** POST /live/channels/{id}/watch: the server's own live HLS playlist. */
@Serializable
data class LiveWatch(val run: String, val playlist: String)
