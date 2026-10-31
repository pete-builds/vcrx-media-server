// Wire types for server/api/openapi.yaml, the contract between the server and every
// client. Field names follow the spec; fields the spec marks optional default here
// so an older or newer server never breaks decoding.
package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class ErrorBody(
    val error: String = "",
    val message: String = "",
)

@Serializable
data class ServerInfo(
    val name: String = "",
    val product: String = "",
    val version: String = "",
    @SerialName("api_version") val apiVersion: Int = 0,
    /** True when this device's network may sign in without a password (POST /api/v1/auth/auto). */
    @SerialName("auto_sign_in") val autoSignIn: Boolean = false,
    /** True when this network gets "Who's watching?" (GET /api/v1/profiles) instead of auto sign-in. */
    @SerialName("profile_picker") val profilePicker: Boolean = false,
    /** True when the admin turned on YouTube trailers, so details may carry one with source "youtube". */
    @SerialName("trailers_youtube") val trailersYoutube: Boolean = false,
)

@Serializable
data class Device(
    val id: String,
    val name: String,
    val model: String? = null,
    val platform: String,
    val client: String,
    @SerialName("client_version") val clientVersion: String,
)

@Serializable
data class LoginRequest(
    val username: String,
    val password: String,
    val device: Device,
)

@Serializable
data class AutoLoginRequest(val device: Device)

@Serializable
data class User(
    val id: Long,
    val username: String,
    @SerialName("display_name") val displayName: String? = null,
    @SerialName("is_admin") val isAdmin: Boolean = false,
)

@Serializable
data class LoginResponse(
    val token: String,
    @SerialName("expires_at") val expiresAt: Long = 0,
    val user: User,
)

@Serializable
data class Library(
    val id: Long,
    val name: String,
    val kind: String,
    @SerialName("item_count") val itemCount: Int = 0,
)

@Serializable
data class LibrariesResponse(val libraries: List<Library> = emptyList())

@Serializable
data class WatchState(
    @SerialName("position_ms") val positionMs: Long = 0,
    @SerialName("duration_ms") val durationMs: Long? = null,
    val played: Boolean = false,
    @SerialName("play_count") val playCount: Int = 0,
    @SerialName("last_played_at") val lastPlayedAt: Long? = null,
    @SerialName("unplayed_count") val unplayedCount: Int? = null,
)

@Serializable
data class Track(
    val id: Long,
    val kind: String,
    val index: Int? = null,
    val codec: String? = null,
    val language: String? = null,
    val title: String? = null,
    val channels: Int? = null,
    @SerialName("is_default") val isDefault: Boolean = false,
    @SerialName("is_forced") val isForced: Boolean = false,
    @SerialName("is_external") val isExternal: Boolean = false,
    @SerialName("is_image_based") val isImageBased: Boolean = false,
)

@Serializable
data class Version(
    val id: Long,
    val label: String? = null,
    @SerialName("file_name") val fileName: String = "",
    val container: String? = null,
    val size: Long = 0,
    val bitrate: Long? = null,
    @SerialName("duration_ms") val durationMs: Long? = null,
    @SerialName("video_codec") val videoCodec: String? = null,
    @SerialName("video_profile") val videoProfile: String? = null,
    @SerialName("bit_depth") val bitDepth: Int? = null,
    val width: Int? = null,
    val height: Int? = null,
    val resolution: String? = null,
    @SerialName("hdr_type") val hdrType: String? = null,
    @SerialName("audio_tracks") val audioTracks: List<Track> = emptyList(),
    @SerialName("subtitle_tracks") val subtitleTracks: List<Track> = emptyList(),
)

/**
 * ItemSummary and ItemDetail in one type: every detail field is optional in the
 * spec, so a summary decodes with those fields at their defaults.
 */
@Serializable
data class Item(
    val id: Long,
    val kind: String,
    @SerialName("library_id") val libraryId: Long = 0,
    val title: String,
    val year: Int? = null,
    @SerialName("parent_id") val parentId: Long? = null,
    @SerialName("show_id") val showId: Long? = null,
    @SerialName("show_title") val showTitle: String? = null,
    @SerialName("season_number") val seasonNumber: Int? = null,
    @SerialName("episode_number") val episodeNumber: Int? = null,
    @SerialName("episode_number_end") val episodeNumberEnd: Int? = null,
    @SerialName("runtime_ms") val runtimeMs: Long? = null,
    val resolution: String? = null,
    @SerialName("child_count") val childCount: Int? = null,
    @SerialName("has_poster") val hasPoster: Boolean = false,
    @SerialName("has_backdrop") val hasBackdrop: Boolean = false,
    @SerialName("has_thumb") val hasThumb: Boolean = false,
    @SerialName("added_at") val addedAt: Long = 0,
    val watch: WatchState? = null,
    val overview: String? = null,
    val tagline: String? = null,
    @SerialName("content_rating") val contentRating: String? = null,
    val genres: List<String> = emptyList(),
    val directors: List<String> = emptyList(),
    val writers: List<String> = emptyList(),
    val studios: List<String> = emptyList(),
    val countries: List<String> = emptyList(),
    /** Release or first air date, YYYY-MM-DD. */
    @SerialName("premiere_date") val premiereDate: String? = null,
    /** Actors in billing order; an episode's guests first. */
    val cast: List<CastMember> = emptyList(),
    val ratings: Map<String, Double> = emptyMap(),
    /** How many votes stand behind each of ratings, when known. */
    @SerialName("rating_votes") val ratingVotes: Map<String, Long> = emptyMap(),
    val versions: List<Version> = emptyList(),
    @SerialName("next_episode") val nextEpisode: Item? = null,
    /** Collections this item is in, such as a film series. */
    val collections: List<CollectionRef> = emptyList(),
    /** A short line some Home rows add, such as "3 new episodes"; null elsewhere. */
    val note: String? = null,
    /** A movie's or show's local trailers, featurettes and the like, trailers first. */
    val extras: List<Extra> = emptyList(),
    /** What the Trailer button plays: a local extra first, else YouTube when the server offers it. */
    val trailer: Trailer? = null,
    /** A show's theme.mp3. */
    @SerialName("theme_music") val themeMusic: ThemeMusic? = null,
)

@Serializable
data class CastMember(val name: String, val role: String? = null)

@Serializable
data class Extra(
    val id: Long,
    val kind: String = "other",
    val title: String = "",
    @SerialName("duration_ms") val durationMs: Long? = null,
)

@Serializable
data class Trailer(
    /** "local" or "youtube". */
    val source: String,
    val title: String = "Trailer",
    @SerialName("extra_id") val extraId: Long? = null,
    @SerialName("youtube_key") val youtubeKey: String? = null,
)

@Serializable
data class ThemeMusic(val id: Long, val url: String)

@Serializable
data class CollectionRef(val id: Long, val name: String)

@Serializable
data class CollectionInfo(
    val id: Long,
    @SerialName("library_id") val libraryId: Long = 0,
    val name: String,
    @SerialName("item_count") val itemCount: Int = 0,
)

@Serializable
data class CollectionResponse(val collection: CollectionInfo, val items: List<Item> = emptyList())

@Serializable
data class ItemPage(
    val items: List<Item> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class ItemsResponse(val items: List<Item> = emptyList())

/**
 * One Home row. The server decides which rows exist and their order (the
 * user's Home layout); the app draws whatever comes. [id] is stable and is the
 * key for GET /api/v1/rows/{id} when [seeAll] is true.
 */
@Serializable
data class HomeRow(
    val id: String,
    val title: String,
    val kind: String = "",
    @SerialName("library_id") val libraryId: Long? = null,
    @SerialName("see_all") val seeAll: Boolean = false,
    val items: List<Item> = emptyList(),
)

@Serializable
data class HomeResponse(val rows: List<HomeRow> = emptyList())

/** One slot of the Home layout: a kind of row, whether it shows, and its place. */
@Serializable
data class HomeLayoutRow(
    val key: String,
    val title: String = "",
    val description: String = "",
    val enabled: Boolean = true,
)

/** The caller's Home layout; [source] is "user", "server" or "default". */
@Serializable
data class HomeLayout(
    val rows: List<HomeLayoutRow> = emptyList(),
    val source: String = "default",
)

@Serializable
data class HomeLayoutEntry(val key: String, val enabled: Boolean)

@Serializable
data class HomeLayoutInput(val rows: List<HomeLayoutEntry>)

@Serializable
data class SetWatchedRequest(val played: Boolean)

@Serializable
data class VideoCodecCapability(
    val codec: String,
    val profiles: List<String> = emptyList(),
    @SerialName("max_bit_depth") val maxBitDepth: Int? = null,
    @SerialName("max_width") val maxWidth: Int? = null,
    @SerialName("max_height") val maxHeight: Int? = null,
    @SerialName("max_fps") val maxFps: Double? = null,
)

@Serializable
data class AudioCodecCapability(
    val codec: String,
    @SerialName("max_channels") val maxChannels: Int? = null,
    val decoder: String? = null,
)

@Serializable
data class Capabilities(
    val containers: List<String>,
    @SerialName("video_codecs") val videoCodecs: List<VideoCodecCapability>,
    @SerialName("audio_codecs") val audioCodecs: List<AudioCodecCapability>,
    @SerialName("hdr_types") val hdrTypes: List<String> = emptyList(),
    @SerialName("subtitle_codecs") val subtitleCodecs: List<String> = emptyList(),
    @SerialName("max_width") val maxWidth: Int? = null,
    @SerialName("max_height") val maxHeight: Int? = null,
    @SerialName("max_bitrate") val maxBitrate: Long? = null,
    /** This client plays a converted fragmented MP4 stream and seeks it through the seek endpoint. */
    @SerialName("fmp4_stream") val fmp4Stream: Boolean = false,
)

@Serializable
data class PlaybackRequest(
    @SerialName("item_id") val itemId: Long,
    @SerialName("version_id") val versionId: Long? = null,
    @SerialName("start_position_ms") val startPositionMs: Long? = null,
    val capabilities: Capabilities,
    /** Plays one of the item's extras (a trailer) instead of a version. */
    @SerialName("extra_id") val extraId: Long? = null,
)

@Serializable
data class SubtitleSource(
    @SerialName("track_id") val trackId: Long,
    val url: String,
    val codec: String = "",
    @SerialName("mime_type") val mimeType: String,
    val language: String? = null,
    val title: String? = null,
    @SerialName("is_forced") val isForced: Boolean = false,
    /** A track inside the media file; its WebVTT copy may answer 202 while the server reads it out. */
    val embedded: Boolean = false,
)

@Serializable
data class StreamInfo(
    val url: String,
    val protocol: String = "progressive",
    @SerialName("mime_type") val mimeType: String? = null,
    /** For protocol fmp4: the seek endpoint, asked with position_ms before every stream. */
    @SerialName("seek_url") val seekUrl: String? = null,
)

/** The seek endpoint's answer: the converted stream asked for starts at [startMs] in the file. */
@Serializable
data class ConvertedStart(
    @SerialName("start_ms") val startMs: Long,
    val url: String,
)

@Serializable
data class PlaybackSession(
    @SerialName("session_id") val sessionId: String,
    val decision: String = "",
    val reason: String = "",
    @SerialName("item_id") val itemId: Long,
    val version: Version,
    val stream: StreamInfo,
    val subtitles: List<SubtitleSource> = emptyList(),
    /**
     * Every text track, sidecar or embedded, as WebVTT. A converted stream carries no
     * subtitles of its own, so the app shows these itself, timed to the file.
     */
    @SerialName("webvtt_subtitles") val webVttSubtitles: List<SubtitleSource> = emptyList(),
    @SerialName("resume_position_ms") val resumePositionMs: Long = 0,
    @SerialName("heartbeat_interval_ms") val heartbeatIntervalMs: Long = 10_000,
    /** Intro and credits, for Skip Intro and Skip Credits (player/SkipMarkers.kt). */
    val markers: List<Marker> = emptyList(),
    /** This user's choice: button, auto or off. Set with [ServerApi.setPlaybackSettings]. */
    @SerialName("skip_markers") val skipMarkers: String = PlaybackSettings.BUTTON,
)

@Serializable
data class PlaybackEvent(
    val type: String,
    @SerialName("position_ms") val positionMs: Long,
    @SerialName("from_position_ms") val fromPositionMs: Long? = null,
    @SerialName("duration_ms") val durationMs: Long? = null,
    val message: String? = null,
)

/** One open playback session, as GET /api/v1/admin/activity/now lists it. */
@Serializable
data class NowSession(
    val id: String,
    val state: String = "",
    val decision: String = "",
    val username: String? = null,
    @SerialName("item_title") val itemTitle: String? = null,
    @SerialName("device_name") val deviceName: String? = null,
    val client: String? = null,
    @SerialName("version_summary") val versionSummary: String? = null,
    @SerialName("position_ms") val positionMs: Long = 0,
    @SerialName("duration_ms") val durationMs: Long? = null,
)

@Serializable
data class NowPlaying(
    val sessions: List<NowSession> = emptyList(),
    @SerialName("stream_count") val streamCount: Int = 0,
)

@Serializable
data class TerminateRequest(val message: String)

/** A stretch of an episode a player may offer to skip. */
@Serializable
data class Marker(
    /** intro or credits */
    val kind: String,
    @SerialName("start_ms") val startMs: Long,
    @SerialName("end_ms") val endMs: Long,
    val confidence: Double = 1.0,
    val source: String = "",
    /** The server says to skip this one without asking (a confident intro, with skip_markers auto). */
    @SerialName("auto_skip") val autoSkip: Boolean = false,
)

/**
 * The signed-in user's own playback choices, stored on the server and shared with
 * the web app: GET and PUT /api/v1/me/playback-settings.
 */
@Serializable
data class PlaybackSettings(
    /** Intros: [BUTTON] (the default), [AUTO] (confident intros only) or [OFF]. */
    @SerialName("skip_markers") val skipMarkers: String = BUTTON,
) {
    companion object {
        const val BUTTON = "button"
        const val AUTO = "auto"
        const val OFF = "off"
    }
}
