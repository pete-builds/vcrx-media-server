// Wire types for the music routes in server/api/openapi.yaml (/api/v1/music/...).
// As in Models.kt, optional fields default so a newer server never breaks decoding.
package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class MusicArtist(
    val id: Long,
    val name: String,
    @SerialName("library_id") val libraryId: Long = 0,
    @SerialName("album_count") val albumCount: Int = 0,
    @SerialName("track_count") val trackCount: Int = 0,
    @SerialName("has_image") val hasImage: Boolean = false,
)

@Serializable
data class MusicAlbum(
    val id: Long,
    val title: String,
    @SerialName("artist_id") val artistId: Long = 0,
    @SerialName("artist_name") val artistName: String = "",
    val year: Int? = null,
    val date: String? = null,
    val genre: String? = null,
    val compilation: Boolean = false,
    val live: Boolean = false,
    val venue: String? = null,
    val location: String? = null,
    val source: String? = null,
    @SerialName("track_count") val trackCount: Int = 0,
    @SerialName("duration_ms") val durationMs: Long = 0,
    @SerialName("has_cover") val hasCover: Boolean = false,
)

@Serializable
data class ReplayGain(
    @SerialName("track_gain") val trackGain: Double? = null,
    @SerialName("album_gain") val albumGain: Double? = null,
)

/** How this device plays a track: the file itself, or a stream converted on the fly. */
@Serializable
data class MusicStream(
    val url: String,
    val protocol: String = "progressive",
    @SerialName("mime_type") val mimeType: String? = null,
    val decision: String = "",
    val reason: String = "",
) {
    /** A converted stream answers no byte ranges, so it cannot seek in place. */
    val seekable: Boolean get() = protocol != "converted"
}

@Serializable
data class MusicRefusal(val code: String = "", val message: String = "")

@Serializable
data class MusicTrack(
    val id: Long,
    val title: String,
    @SerialName("album_id") val albumId: Long = 0,
    @SerialName("artist_id") val artistId: Long = 0,
    @SerialName("artist_name") val artistName: String = "",
    @SerialName("album_title") val albumTitle: String = "",
    @SerialName("album_artist") val albumArtist: String = "",
    @SerialName("disc_number") val discNumber: Int? = null,
    @SerialName("track_number") val trackNumber: Int? = null,
    @SerialName("duration_ms") val durationMs: Long? = null,
    val codec: String? = null,
    @SerialName("has_cover") val hasCover: Boolean = false,
    val replaygain: ReplayGain = ReplayGain(),
    /** Set in a play queue: how to play it, or why it cannot. */
    val stream: MusicStream? = null,
    val refusal: MusicRefusal? = null,
)

@Serializable
data class MusicAlbumDetail(
    val id: Long,
    val title: String,
    @SerialName("artist_id") val artistId: Long = 0,
    @SerialName("artist_name") val artistName: String = "",
    val year: Int? = null,
    val date: String? = null,
    val genre: String? = null,
    val compilation: Boolean = false,
    val live: Boolean = false,
    val venue: String? = null,
    val location: String? = null,
    val source: String? = null,
    @SerialName("source_detail") val sourceDetail: String? = null,
    val lineage: String? = null,
    val taper: String? = null,
    @SerialName("duration_ms") val durationMs: Long = 0,
    @SerialName("has_cover") val hasCover: Boolean = false,
    val tracks: List<MusicTrack> = emptyList(),
)

@Serializable
data class MusicArtistDetail(
    val id: Long,
    val name: String,
    @SerialName("album_count") val albumCount: Int = 0,
    @SerialName("track_count") val trackCount: Int = 0,
    @SerialName("has_image") val hasImage: Boolean = false,
    val albums: List<MusicAlbum> = emptyList(),
    val live: List<MusicAlbum> = emptyList(),
    @SerialName("appears_on") val appearsOn: List<MusicAlbum> = emptyList(),
)

@Serializable
data class MusicArtistPage(val artists: List<MusicArtist> = emptyList(), val total: Int = 0)

@Serializable
data class MusicAlbumPage(val albums: List<MusicAlbum> = emptyList(), val total: Int = 0)

@Serializable
data class MusicQueueRequest(
    @SerialName("album_id") val albumId: Long? = null,
    @SerialName("artist_id") val artistId: Long? = null,
    @SerialName("track_ids") val trackIds: List<Long>? = null,
    val shuffle: Boolean? = null,
    val capabilities: Capabilities,
)

@Serializable
data class MusicQueueResponse(val tracks: List<MusicTrack> = emptyList())

@Serializable
data class MusicListenRequest(val capabilities: Capabilities)

/** A listen in Activity; events go to the playback events route as for video. */
@Serializable
data class MusicListen(
    @SerialName("session_id") val sessionId: String,
    @SerialName("track_id") val trackId: Long = 0,
    val decision: String = "",
    @SerialName("heartbeat_interval_ms") val heartbeatIntervalMs: Long = 10_000,
)

/** "2024-06-14 · The Lantern Room" for a show, the year for an album. */
fun MusicAlbum.line(withArtist: Boolean = true): String = listOfNotNull(
    artistName.takeIf { withArtist && it.isNotEmpty() },
    if (live) date else year?.toString(),
    venue.takeIf { live },
).joinToString(" · ")

/**
 * What a TV plays for music: the codecs the video probe found (Media3 decodes
 * FLAC, MP3, AAC, Opus and Vorbis, platform or bundled FFmpeg), plus PCM in WAV,
 * which Media3 reads itself, and the audio containers its extractors know. SHN,
 * AIFF and WavPack are not among them, so those play converted.
 */
fun musicCapabilities(video: Capabilities): Capabilities = Capabilities(
    containers = listOf("flac", "mp3", "m4a", "mp4", "ogg", "opus", "wav", "aac", "mka", "webm"),
    videoCodecs = emptyList(),
    audioCodecs = (video.audioCodecs + AudioCodecCapability(codec = "pcm", maxChannels = 8)).distinctBy { it.codec },
)
