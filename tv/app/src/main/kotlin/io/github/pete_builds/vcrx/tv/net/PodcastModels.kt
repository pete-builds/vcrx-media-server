// Wire types for the podcast routes in server/api/openapi.yaml (/api/v1/podcasts/...).
// As in Models.kt, optional fields default so a newer server never breaks decoding,
// and an older server without podcasts never sends any of this (User.podcasts stays false).
package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** A show the user subscribes to (on the web or the phone: the TV never subscribes). */
@Serializable
data class Podcast(
    val id: Long,
    val title: String = "",
    val author: String = "",
    val description: String = "",
    val explicit: Boolean = false,
    @SerialName("has_artwork") val hasArtwork: Boolean = false,
    @SerialName("artwork_url") val artworkUrl: String? = null,
    @SerialName("episode_count") val episodeCount: Int = 0,
    @SerialName("unplayed_count") val unplayedCount: Int = 0,
    @SerialName("new_count") val newCount: Int = 0,
    @SerialName("latest_published_at") val latestPublishedAt: Long? = null,
    @SerialName("last_error") val lastError: String? = null,
)

/** One episode with the user's own place in it. Its audio only ever comes through VCRX ([streamUrl]). */
@Serializable
data class PodcastEpisode(
    val id: Long,
    @SerialName("podcast_id") val podcastId: Long = 0,
    @SerialName("podcast_title") val podcastTitle: String = "",
    val title: String = "",
    val description: String = "",
    @SerialName("published_at") val publishedAt: Long? = null,
    @SerialName("duration_seconds") val durationSeconds: Long? = null,
    val season: Int? = null,
    val episode: Int? = null,
    @SerialName("episode_type") val episodeType: String = "full",
    val explicit: Boolean = false,
    @SerialName("mime_type") val mimeType: String? = null,
    @SerialName("stream_url") val streamUrl: String = "",
    @SerialName("artwork_url") val artworkUrl: String? = null,
    @SerialName("position_seconds") val positionSeconds: Double = 0.0,
    val played: Boolean = false,
    val new: Boolean = false,
)

@Serializable
data class PodcastsResponse(val podcasts: List<Podcast> = emptyList())

@Serializable
data class PodcastShowResponse(
    val podcast: Podcast,
    val episodes: List<PodcastEpisode> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class PodcastProgress(
    @SerialName("position_seconds") val positionSeconds: Double = 0.0,
    val played: Boolean = false,
    @SerialName("duration_seconds") val durationSeconds: Long? = null,
)

/** PUT .../progress. [durationSeconds] is what the player measured, for feeds that give none. */
@Serializable
data class PodcastProgressRequest(
    @SerialName("position_seconds") val positionSeconds: Double,
    @SerialName("duration_seconds") val durationSeconds: Double? = null,
)

/** PUT .../played. */
@Serializable
data class PodcastPlayedRequest(val played: Boolean)
