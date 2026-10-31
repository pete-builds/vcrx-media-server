package io.github.pete_builds.vcrx.tv.ui.settings

import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.media3.common.text.Cue
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.SubtitleView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.HomeRow
import io.github.pete_builds.vcrx.tv.player.CaptionStyles
import io.github.pete_builds.vcrx.tv.settings.Choices
import io.github.pete_builds.vcrx.tv.settings.Describe
import io.github.pete_builds.vcrx.tv.settings.FeatureHooks
import io.github.pete_builds.vcrx.tv.settings.HomeLayout
import io.github.pete_builds.vcrx.tv.settings.Languages
import io.github.pete_builds.vcrx.tv.settings.PlaybackPrefs
import io.github.pete_builds.vcrx.tv.settings.Preferences
import io.github.pete_builds.vcrx.tv.settings.QualityCap
import io.github.pete_builds.vcrx.tv.settings.ResumeMode
import io.github.pete_builds.vcrx.tv.settings.SkipIntroMode
import io.github.pete_builds.vcrx.tv.settings.SubtitleBackground
import io.github.pete_builds.vcrx.tv.settings.SubtitleColor
import io.github.pete_builds.vcrx.tv.settings.SubtitleEdge
import io.github.pete_builds.vcrx.tv.settings.SubtitleMode
import io.github.pete_builds.vcrx.tv.settings.SubtitlePosition
import io.github.pete_builds.vcrx.tv.settings.SubtitleSize
import io.github.pete_builds.vcrx.tv.settings.SubtitleStyle
import io.github.pete_builds.vcrx.tv.settings.TrailerMode
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandDialog
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import java.util.Locale
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/** The pages of Settings, in the order the Settings section lists them. */
enum class SettingsPage(val title: String) {
    Subtitles("Subtitles"),
    Playback("Playback"),
    Audio("Audio"),
    Video("Video"),
    Home("Home screen"),
    Account("Account and server"),
    About("About and diagnostics"),
}

@Composable
fun SettingsScreen(page: String) {
    when (SettingsPage.entries.firstOrNull { it.name == page } ?: SettingsPage.Subtitles) {
        SettingsPage.Subtitles -> SubtitleSettings()
        SettingsPage.Playback -> PlaybackSettings()
        SettingsPage.Audio -> AudioSettings()
        SettingsPage.Video -> VideoSettings()
        SettingsPage.Home -> HomeSettings()
        SettingsPage.Account -> AccountSettings()
        SettingsPage.About -> AboutScreen()
    }
}

/** The settings and a way to change them; Compose sees each change at once. */
@Composable
private fun rememberPrefs(): Pair<Preferences, ((Preferences) -> Preferences) -> Unit> {
    val store = LocalContext.current.graph.preferences
    val prefs by store.flow.collectAsState()
    val update: ((Preferences) -> Preferences) -> Unit = { change -> store.update(change) }
    return prefs to update
}

private val deviceLanguage: String get() = Locale.getDefault().language

/**
 * A page: its title and a column of rows. [first] goes on the first row, which
 * takes focus when the page opens. The TV screen is 960dp wide; [width] leaves room.
 */
@Composable
private fun SettingsPageFrame(
    title: String,
    modifier: Modifier = Modifier,
    width: Dp = 760.dp,
    content: LazyListScope.(first: Modifier) -> Unit,
) {
    val first = remember { FocusRequester() }
    LazyColumn(
        modifier.width(width).fillMaxHeight(),
        contentPadding = PaddingValues(start = 48.dp, end = 24.dp, top = 40.dp, bottom = 48.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        item(key = "title") {
            Column(Modifier.padding(start = 16.dp, bottom = 16.dp)) {
                Label("Settings", style = MaterialTheme.typography.labelMedium, color = Gray)
                Text(title, style = MaterialTheme.typography.headlineSmall)
            }
        }
        content(Modifier.focusRequester(first))
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}

private val LANGUAGE_OPTIONS = listOf("") + Languages.COMMON

@Composable
private fun SubtitleSettings() {
    val (prefs, update) = rememberPrefs()
    val store = LocalContext.current.graph.preferences
    val subs = prefs.subtitles
    val style = subs.style
    var remembered by remember { mutableIntStateOf(store.rememberedCount()) }
    fun setStyle(change: (SubtitleStyle) -> SubtitleStyle) = update { it.copy(subtitles = it.subtitles.copy(style = change(it.subtitles.style))) }

    Row(Modifier.fillMaxSize()) {
        SettingsPageFrame("Subtitles", width = 580.dp) { first ->
            item(key = "enabled") {
                ToggleRow(
                    "Subtitles",
                    subs.enabled,
                    "The default for every video. Off shows none at all, forced lines included.",
                    onChange = { on ->
                        update { it.copy(subtitles = it.subtitles.copy(enabled = on)) }
                        remembered = store.rememberedCount()
                    },
                    modifier = first,
                )
            }
            item(key = "mode") {
                ChoiceRow(
                    "When on, show",
                    subs.mode,
                    SubtitleMode.entries,
                    Describe::mode,
                    Describe.modeHelp(subs.mode),
                    onPick = { mode ->
                        update { it.copy(subtitles = it.subtitles.copy(mode = mode)) }
                        remembered = store.rememberedCount()
                    },
                    enabled = subs.enabled,
                )
            }
            item(key = "language1") {
                ChoiceRow(
                    "Language",
                    subs.languages.getOrElse(0) { "" },
                    LANGUAGE_OPTIONS,
                    { Describe.language(it, deviceLanguage) },
                    "The subtitle language to look for first.",
                    onPick = { code ->
                        update { it.copy(subtitles = it.subtitles.copy(languages = languages(code, it.subtitles.languages.getOrElse(1) { "" }))) }
                        remembered = store.rememberedCount()
                    },
                )
            }
            item(key = "language2") {
                ChoiceRow(
                    "Second language",
                    subs.languages.getOrElse(1) { "" },
                    LANGUAGE_OPTIONS,
                    { if (it.isBlank()) "None" else Languages.name(it) },
                    "Used when a title has nothing in the first language.",
                    onPick = { code ->
                        update { it.copy(subtitles = it.subtitles.copy(languages = languages(it.subtitles.languages.getOrElse(0) { "" }, code))) }
                        remembered = store.rememberedCount()
                    },
                    enabled = subs.languages.isNotEmpty(),
                )
            }
            item(key = "forget") {
                SettingRow(
                    "Forget choices made while watching",
                    if (remembered == 0) "None" else "$remembered titles and shows",
                    "Subtitles picked during playback stick to that title and show until this, or a change above.",
                    onClick = {
                        store.forgetSubtitleChoices()
                        remembered = 0
                    },
                )
            }
            item(key = "style-heading") { SectionLabel("Style") }
            item(key = "custom") {
                ChoiceRow(
                    "Style",
                    style.custom,
                    listOf(false, true),
                    { if (it) "Custom" else "TV caption settings" },
                    "TV caption settings follow Android's Accessibility > Captions.",
                    onPick = { custom -> setStyle { it.copy(custom = custom) } },
                )
            }
            item(key = "size") {
                ChoiceRow("Size", style.size, SubtitleSize.entries, Describe::size, "How large the letters are.", { v -> setStyle { it.copy(size = v) } }, enabled = style.custom)
            }
            item(key = "color") {
                ChoiceRow("Colour", style.color, SubtitleColor.entries, Describe::color, "The colour of the letters.", { v -> setStyle { it.copy(color = v) } }, enabled = style.custom)
            }
            item(key = "background") {
                ChoiceRow(
                    "Background",
                    style.background,
                    SubtitleBackground.entries,
                    Describe::background,
                    "A box behind each line, for bright scenes.",
                    { v -> setStyle { it.copy(background = v) } },
                    enabled = style.custom,
                )
            }
            item(key = "edge") {
                ChoiceRow("Edge", style.edge, SubtitleEdge.entries, Describe::edge, "What sets the letters off from the picture.", { v -> setStyle { it.copy(edge = v) } }, enabled = style.custom)
            }
            item(key = "position") {
                ChoiceRow(
                    "Position",
                    style.position,
                    SubtitlePosition.entries,
                    Describe::position,
                    "Raise them clear of a TV's own on-screen bars.",
                    { v -> setStyle { it.copy(position = v) } },
                    enabled = style.custom,
                )
            }
        }
        SubtitlePreview(style, Modifier.padding(top = 120.dp, end = 32.dp))
    }
}

/** The first and second subtitle language as the stored list: empty means the TV's language. */
private fun languages(first: String, second: String): List<String> =
    if (first.isBlank()) emptyList() else listOf(first, second).filter { it.isNotBlank() }.distinct()

/** A sample line over a stand-in picture, drawn by the same view and style the player uses. */
@OptIn(UnstableApi::class)
@Composable
private fun SubtitlePreview(style: SubtitleStyle, modifier: Modifier = Modifier) {
    val sample = remember { listOf(Cue.Builder().setText("Every subtitle will look like this.").build()) }
    Column(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Label("Preview", style = MaterialTheme.typography.labelMedium, color = Gray)
        Box(Modifier.width(320.dp).aspectRatio(16f / 9f).background(PreviewPicture, RoundedCornerShape(8.dp))) {
            AndroidView(
                modifier = Modifier.fillMaxSize(),
                factory = { SubtitleView(it) },
                update = { view ->
                    CaptionStyles.apply(view, style)
                    view.setCues(sample)
                },
            )
        }
    }
}

/** A mid-tone, so both light and dark letters and every background can be judged. */
private val PreviewPicture = Color(0xFF51606E)

@Composable
private fun PlaybackSettings() {
    val (prefs, update) = rememberPrefs()
    val p = prefs.playback
    fun set(change: (PlaybackPrefs) -> PlaybackPrefs) = update { it.copy(playback = change(it.playback)) }
    SettingsPageFrame("Playback") { first ->
        item(key = "autoplay") {
            ToggleRow("Play the next episode", p.autoPlayNext, "At the end of an episode, start the next after a countdown.", { v -> set { it.copy(autoPlayNext = v) } }, first)
        }
        item(key = "countdown") {
            ChoiceRow(
                "Countdown",
                p.countdownSeconds,
                Choices.COUNTDOWN_SECONDS,
                Describe::seconds,
                "How long Up Next waits before it plays.",
                { v -> set { it.copy(countdownSeconds = v) } },
                enabled = p.autoPlayNext,
            )
        }
        item(key = "resume") {
            ChoiceRow(
                "Resume",
                p.resume,
                ResumeMode.entries,
                Describe::resume,
                "What Play does with something you stopped part way through.",
                { v -> set { it.copy(resume = v) } },
            )
        }
        item(key = "back") {
            ChoiceRow("Skip back", p.skipBackSeconds, Choices.SKIP_SECONDS, Describe::seconds, "One press of left, or the Back button.", { v -> set { it.copy(skipBackSeconds = v) } })
        }
        item(key = "forward") {
            ChoiceRow("Skip forward", p.skipForwardSeconds, Choices.SKIP_SECONDS, Describe::seconds, "One press of right, or the Forward button.", { v -> set { it.copy(skipForwardSeconds = v) } })
        }
        item(key = "quality") {
            ChoiceRow(
                "Maximum streaming quality",
                p.quality,
                QualityCap.entries,
                Describe::quality,
                "Larger files are converted down by the server, when it can convert video.",
                { v -> set { it.copy(quality = v) } },
            )
        }
        if (FeatureHooks.SKIP_INTRO) {
            item(key = "intro") {
                ChoiceRow("Skip intro", prefs.skipIntro, SkipIntroMode.entries, Describe::skipIntro, "What happens when an intro starts.", { v -> update { it.copy(skipIntro = v) } })
            }
        }
        if (FeatureHooks.TRAILERS) {
            item(key = "trailers") {
                ChoiceRow("Trailers", prefs.trailers, TrailerMode.entries, Describe::trailers, "Where trailers come from.", { v -> update { it.copy(trailers = v) } })
            }
        }
    }
}

@Composable
private fun AudioSettings() {
    val (prefs, update) = rememberPrefs()
    val a = prefs.audio
    SettingsPageFrame("Audio") { first ->
        item(key = "original") {
            ToggleRow(
                "Prefer the original language",
                a.preferOriginal,
                "Play each file's own main track, whatever its language.",
                { v -> update { it.copy(audio = it.audio.copy(preferOriginal = v)) } },
                first,
            )
        }
        item(key = "language") {
            ChoiceRow(
                "Preferred language",
                a.language,
                LANGUAGE_OPTIONS,
                { Describe.language(it, deviceLanguage) },
                "Chosen when a file has this language, with the original preference off.",
                { v -> update { it.copy(audio = it.audio.copy(language = v)) } },
                enabled = !a.preferOriginal,
            )
        }
        item(key = "passthrough") {
            ToggleRow(
                "Passthrough to a receiver",
                a.passthrough,
                "Off: the TV decodes Dolby and DTS itself. Try it if a soundbar is silent.",
                { v -> update { it.copy(audio = it.audio.copy(passthrough = v)) } },
            )
        }
    }
}

@Composable
private fun VideoSettings() {
    val (prefs, update) = rememberPrefs()
    val v = prefs.video
    SettingsPageFrame("Video") { first ->
        item(key = "hdr") {
            ToggleRow(
                "HDR10, HDR10+ and HLG",
                v.hdr,
                "Off tells the server this TV shows no HDR; it sends SDR when it can convert.",
                { on -> update { it.copy(video = it.video.copy(hdr = on)) } },
                first,
            )
        }
        item(key = "dv") {
            ToggleRow(
                "Dolby Vision",
                v.dolbyVision,
                "Off for a TV that shows Dolby Vision with the wrong colours.",
                { on -> update { it.copy(video = it.video.copy(dolbyVision = on)) } },
            )
        }
    }
}

class HomeRowsViewModel(private val graph: AppGraph) : ViewModel() {
    var rows by mutableStateOf<LoadState<List<HomeRow>>>(LoadState.Loading)
        private set

    init {
        load()
    }

    fun load() {
        rows = LoadState.Loading
        viewModelScope.launch {
            rows = try {
                LoadState.Ready(graph.api.home())
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

@Composable
private fun HomeSettings() {
    val (prefs, update) = rememberPrefs()
    val vm = graphViewModel(key = "settings-home") { HomeRowsViewModel(it) }
    var editing by remember { mutableStateOf<HomeRow?>(null) }
    LoadStateContent(vm.rows, onRetry = vm::load) { rows ->
        val ids = rows.map { it.id }
        val ordered = HomeLayout.ordered(ids, prefs.home).mapNotNull { id -> rows.firstOrNull { it.id == id } }
        SettingsPageFrame("Home screen") { first ->
            items(ordered, key = { it.id }) { row ->
                SettingRow(
                    row.title,
                    if (row.id in prefs.home.hidden) "Hidden" else "Shown",
                    "Press to show, hide or move this row.",
                    onClick = { editing = row },
                    modifier = if (row.id == ordered.first().id) first else Modifier,
                )
            }
            item(key = "reset") {
                SettingRow("Reset", null, "Show every row in the server's order.", onClick = { update { it.copy(home = it.home.copy(hidden = emptyList(), order = emptyList())) } })
            }
        }
        editing?.let { row ->
            BrandDialog(row.title, onDismiss = { editing = null }) {
                val hidden = row.id in prefs.home.hidden
                BrandRow(onClick = { update { it.copy(home = HomeLayout.toggle(it.home, row.id)) }; editing = null }) {
                    Label(if (hidden) "Show this row" else "Hide this row")
                }
                BrandRow(onClick = { update { it.copy(home = HomeLayout.move(ids, it.home, row.id, -1)) } }) { Label("Move up") }
                BrandRow(onClick = { update { it.copy(home = HomeLayout.move(ids, it.home, row.id, 1)) } }) { Label("Move down") }
            }
        }
    }
}

@Composable
private fun AccountSettings() {
    val graph = LocalContext.current.graph
    val actions = LocalAppActions.current
    val scope = rememberCoroutineScope()
    var signingOut by remember { mutableStateOf(false) }
    SettingsPageFrame("Account and server") { first ->
        item(key = "user") { FactRow("Signed in as", graph.settings.username ?: "Unknown user", first) }
        item(key = "server") {
            FactRow("Server", listOfNotNull(graph.settings.serverName, graph.settings.serverUrl).joinToString("  ").ifEmpty { "None" })
        }
        item(key = "profile") {
            SettingRow(
                "Switch profile",
                null,
                "Who's watching? Pick another household profile, with its PIN if it has one.",
                onClick = { actions.openProfiles() },
            )
        }
        item(key = "switch") {
            SettingRow(
                if (signingOut) "Signing out" else "Switch user or sign out",
                null,
                "Signs out of this TV and shows the sign-in form. Settings stay with each user.",
                onClick = {
                    if (!signingOut) {
                        signingOut = true
                        scope.launch {
                            // Revoke the token on the server if it answers quickly; sign out locally regardless.
                            withTimeoutOrNull(3_000) { runCatching { graph.api.logout() } }
                            graph.tokens.clear()
                            graph.settings.skipAutoSignIn = true
                            actions.toLogin()
                        }
                    }
                },
            )
        }
        item(key = "change") {
            SettingRow("Change server", null, "Connect this TV to a different server address.", onClick = { actions.openConnect() })
        }
    }
}

/** One line per category for the Settings section: what each page is set to now. */
fun summary(page: SettingsPage, prefs: Preferences): String = when (page) {
    SettingsPage.Subtitles -> if (!prefs.subtitles.enabled) "Off" else
        "On, ${Describe.languages(prefs.subtitles.languages, deviceLanguage).lowercase(Locale.ROOT).replaceFirstChar { it.uppercase() }}"
    SettingsPage.Playback -> listOf(
        if (prefs.playback.autoPlayNext) "Next episode plays" else "Next episode waits",
        Describe.resume(prefs.playback.resume),
    ).joinToString(", ")
    SettingsPage.Audio -> listOf(
        if (prefs.audio.preferOriginal) "Original language" else Describe.language(prefs.audio.language, deviceLanguage),
        "passthrough ${Describe.onOff(prefs.audio.passthrough).lowercase(Locale.ROOT)}",
    ).joinToString(", ")
    SettingsPage.Video -> when {
        prefs.video.hdr && prefs.video.dolbyVision -> "HDR and Dolby Vision on"
        !prefs.video.hdr && !prefs.video.dolbyVision -> "HDR and Dolby Vision off"
        prefs.video.hdr -> "HDR on, Dolby Vision off"
        else -> "HDR off, Dolby Vision on"
    }
    SettingsPage.Home -> if (prefs.home.hidden.isEmpty()) "All rows shown" else "${prefs.home.hidden.size} hidden"
    SettingsPage.Account -> ""
    SettingsPage.About -> ""
}

/** The description each category shows when focused. */
fun pageHelp(page: SettingsPage): String = when (page) {
    SettingsPage.Subtitles -> "On or off for every video, languages, and how they look."
    SettingsPage.Playback -> "Next episode, resume, skip steps and streaming quality."
    SettingsPage.Audio -> "Audio language and passthrough to a receiver."
    SettingsPage.Video -> "HDR and Dolby Vision, for a TV that shows them badly."
    SettingsPage.Home -> "Which rows Home shows, and in what order."
    SettingsPage.Account -> "Who is signed in, switch user, change the server."
    SettingsPage.About -> "Versions, what this TV decodes, speed test, image cache."
}
