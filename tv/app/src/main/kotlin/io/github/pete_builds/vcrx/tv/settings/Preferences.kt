// What the viewer chose in Settings, for one server and user. Pure and
// serializable, so the defaults and every rule that reads them are unit tested.
// Every field has a default: a preference saved by an older or newer build
// decodes with what it lacks at the default, never as an error.
package io.github.pete_builds.vcrx.tv.settings

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

@Serializable
data class Preferences(
    val subtitles: SubtitlePrefs = SubtitlePrefs(),
    val playback: PlaybackPrefs = PlaybackPrefs(),
    val audio: AudioPrefs = AudioPrefs(),
    val video: VideoPrefs = VideoPrefs(),
    val home: HomePrefs = HomePrefs(),
    /**
     * Where the Trailer button's trailer may come from (trailers/Trailers.kt).
     * YouTube is offered only when the server has YouTube trailers on, so the
     * default means local and YouTube where the server offers it, else local.
     */
    val trailers: TrailerMode = TrailerMode.LocalAndYouTube,
    /** A show's theme.mp3 plays quietly on its details screen. */
    val themeMusic: Boolean = true,
    /** Mirrors the server's skip_markers (player/SkipMarkers.kt rememberServerSkipIntro), which the player reads from each session. */
    val skipIntro: SkipIntroMode = SkipIntroMode.ShowButton,
) {
    companion object {
        private val json = Json {
            ignoreUnknownKeys = true
            // An enum value this build does not know (saved by a newer one) falls back to the default.
            coerceInputValues = true
            encodeDefaults = true
        }

        fun decode(text: String?): Preferences =
            text?.let { runCatching { json.decodeFromString(serializer(), it) }.getOrNull() } ?: Preferences()

        fun encode(prefs: Preferences): String = json.encodeToString(serializer(), prefs)

        /**
         * True when a change touches what every video opens with: the subtitle switch,
         * mode or languages. Choices remembered during playback are then forgotten, so
         * turning subtitles Off in Settings turns them off everywhere, as it reads.
         */
        fun resetsSubtitleChoices(before: Preferences, after: Preferences): Boolean {
            val a = before.subtitles
            val b = after.subtitles
            return a.enabled != b.enabled || a.mode != b.mode || a.languages != b.languages
        }
    }
}

/**
 * Two features other work is adding. Their settings are stored now so those
 * features only read them; the rows appear in Settings when the flag is true.
 */
object FeatureHooks {
    const val TRAILERS = true
    const val SKIP_INTRO = true
}

@Serializable
enum class TrailerMode { Off, LocalOnly, LocalAndYouTube }

@Serializable
enum class SkipIntroMode { ShowButton, Automatic, Off }

@Serializable
data class SubtitlePrefs(
    /** The default for every video. Off means no subtitle track at all, forced ones included. */
    val enabled: Boolean = true,
    val mode: SubtitleMode = SubtitleMode.ForeignAudio,
    /** Two-letter codes in order of preference; empty means the TV's own language. */
    val languages: List<String> = emptyList(),
    val style: SubtitleStyle = SubtitleStyle(),
)

/** Which track shows when subtitles are On. */
@Serializable
enum class SubtitleMode {
    /** A full track in a preferred language whenever there is one. */
    Always,

    /** Only forced tracks: the signs and foreign-language lines of a film in your language. */
    ForcedOnly,

    /** A full track when the audio is in another language; otherwise forced tracks only. */
    ForeignAudio,
}

@Serializable
data class SubtitleStyle(
    /** False follows the TV's own caption settings (Android Settings > Accessibility > Captions). */
    val custom: Boolean = false,
    val size: SubtitleSize = SubtitleSize.Normal,
    val color: SubtitleColor = SubtitleColor.White,
    val background: SubtitleBackground = SubtitleBackground.None,
    val edge: SubtitleEdge = SubtitleEdge.Outline,
    val position: SubtitlePosition = SubtitlePosition.Bottom,
)

@Serializable
enum class SubtitleSize(val scale: Float) { Small(0.8f), Normal(1f), Large(1.3f), Huge(1.65f) }

@Serializable
enum class SubtitleColor(val argb: Long) {
    White(0xFFFFFFFF),
    Yellow(0xFFFFEB3B),
    Cyan(0xFF4DD0E1),
    Green(0xFF8BC34A),
}

/** Opacity of the black box behind each line. */
@Serializable
enum class SubtitleBackground(val alpha: Int) { None(0), Light(64), Medium(128), Dark(192), Solid(255) }

/** How each letter is set off from the picture; mapped to Media3's CaptionStyleCompat edge types. */
@Serializable
enum class SubtitleEdge { None, Outline, DropShadow, Raised, Depressed }

/** How far above the bottom edge the lines sit, as a fraction of the picture height. */
@Serializable
enum class SubtitlePosition(val bottomFraction: Float) { Bottom(0.08f), Raised(0.18f), High(0.3f) }

@Serializable
data class PlaybackPrefs(
    val autoPlayNext: Boolean = true,
    val countdownSeconds: Int = 10,
    val resume: ResumeMode = ResumeMode.Resume,
    val skipBackSeconds: Int = 10,
    val skipForwardSeconds: Int = 10,
    val quality: QualityCap = QualityCap.Original,
)

/** What a Play that is not an explicit Resume or Play-from-start does with a saved position. */
@Serializable
enum class ResumeMode { Ask, Resume, StartOver }

/**
 * The most the server is asked to send. Sent as max_width, max_height and
 * max_bitrate in the capabilities, so anything larger is converted down (on a
 * server that can convert video) rather than played as is.
 */
@Serializable
enum class QualityCap(val width: Int?, val height: Int?, val bitrate: Long?) {
    Original(null, null, null),
    Uhd4k(3840, 2160, 60_000_000),
    Fhd20(1920, 1080, 20_000_000),
    Fhd8(1920, 1080, 8_000_000),
    Hd4(1280, 720, 4_000_000),
    Sd2(854, 480, 2_000_000),
}

@Serializable
data class AudioPrefs(
    /** Two-letter code; empty means the TV's own language. Used when [preferOriginal] is off. */
    val language: String = "",
    /** Play the file's own default track, which is the original language in almost every release. */
    val preferOriginal: Boolean = true,
    /** Off: no format is sent undecoded to a receiver; the TV decodes everything to PCM. */
    val passthrough: Boolean = true,
)

@Serializable
data class VideoPrefs(
    /** Off: the server is told this TV shows no HDR10, HDR10+ or HLG. */
    val hdr: Boolean = true,
    /** Off: the server is told this TV shows no Dolby Vision. */
    val dolbyVision: Boolean = true,
)

@Serializable
data class HomePrefs(
    /** Row ids the viewer hid. */
    val hidden: List<String> = emptyList(),
    /** Row ids in the viewer's order; rows not listed keep the server's order after these. */
    val order: List<String> = emptyList(),
)

/** The choices offered for the numeric settings, in the order the pickers list them. */
object Choices {
    val COUNTDOWN_SECONDS = listOf(5, 10, 15, 20, 30)
    val SKIP_SECONDS = listOf(5, 10, 15, 30, 60)
}
