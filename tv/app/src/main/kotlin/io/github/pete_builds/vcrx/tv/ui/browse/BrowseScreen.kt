package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.currentStateAsState
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.Border
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Glow
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Library
import io.github.pete_builds.vcrx.tv.ui.components.AppIcons
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadingView
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import io.github.pete_builds.vcrx.tv.ui.theme.AccentTint
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.FocusStroke
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Motion
import io.github.pete_builds.vcrx.tv.ui.theme.Surfaces
import io.github.pete_builds.vcrx.tv.ui.theme.White
import io.github.pete_builds.vcrx.tv.ui.music.MusicSection
import io.github.pete_builds.vcrx.tv.ui.podcasts.PodcastsSection
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

const val HOME = "home"
const val SEARCH = "search"
const val SETTINGS = "settings"
const val ACTIVITY = "activity"
const val LIVE = "live"
const val PODCASTS = "podcasts"
fun librarySection(id: Long) = "library:$id"

/**
 * Where focus should go next inside Browse. Lives in the ViewModel, so it survives
 * a trip to Details and back.
 */
class BrowseFocus {
    /** The rail handed over: the section's first item takes focus once it has loaded. */
    var toContent by mutableStateOf(true)

    /** The item whose card opened Details; that card takes focus again on return. */
    var restoreItemId by mutableStateOf<Long?>(null)

    /** The Home row that card was in, since one title can be in several rows. */
    var restoreRowId by mutableStateOf<String?>(null)
}

class BrowseViewModel(private val graph: AppGraph) : ViewModel() {
    var section by mutableStateOf(HOME)
    val focus = BrowseFocus()
    var libraries by mutableStateOf<LoadState<List<Library>>>(LoadState.Loading)
        private set

    /** Admins get Activity in the rail; the server enforces it either way. */
    var isAdmin by mutableStateOf(false)
        private set

    /** Live TV in the rail: the user may watch it and there are channels. */
    var liveTv by mutableStateOf(false)
        private set

    /** Podcasts in the rail: on for this server and this user (never an invited device). */
    var podcasts by mutableStateOf(false)
        private set

    init {
        loadLibraries()
        viewModelScope.launch {
            val me = try {
                graph.api.me()
            } catch (_: ApiException) {
                null
            }
            isAdmin = me?.isAdmin ?: false
            liveTv = me?.liveTv ?: false
            podcasts = me?.podcasts ?: false
        }
    }

    fun loadLibraries() {
        viewModelScope.launch {
            libraries = try {
                LoadState.Ready(graph.api.libraries())
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

/**
 * Whether Browse is the page in front (its back stack entry is resumed). It
 * is not while Details fades in over it or out of it, for 700ms each way, and
 * focus must not move then (HomeRows.cardTakesFocus). This is the state, for
 * keying effects so they run again when Browse is back in front; it can lag
 * a frame behind a click that navigates away, so each effect checks
 * [inFrontNow] again when it runs.
 */
@Composable
fun browseInFront(): Boolean {
    val state by LocalLifecycleOwner.current.lifecycle.currentStateAsState()
    return state.isAtLeast(Lifecycle.State.RESUMED)
}

/** Whether Browse is in front at this moment. For effects only: never read it during composition. */
fun Lifecycle.inFrontNow(): Boolean = currentState.isAtLeast(Lifecycle.State.RESUMED)

private data class RailEntry(val key: String, val label: String, val icon: ImageVector)

/**
 * The rail is a floating pill, centred top to bottom on the left edge, as in
 * Plex (Pete, 2026-11-03: "the menu on the left is actually a floating centred
 * vertically pill. i like that"). Its width at rest (icons only) and with focus
 * in it (icons and labels), and its gap from the screen's edge. 30% smaller
 * than the first cut at rest (Pete, 2026-11-03), so it takes less of the picture.
 */
private val RAIL_CLOSED = 40.dp
private val RAIL_OPEN = 170.dp
private val RAIL_INSET = 12.dp

/** One rail button: a circle at rest, a pill with its label when the rail is open. */
private val RAIL_ITEM = 30.dp

/** A capsule at rest (radius half its width); the same corners, gentler, once it widens. */
private val RAIL_SHAPE = RoundedCornerShape(RAIL_CLOSED / 2)
private val RAIL_ITEM_SHAPE = RoundedCornerShape(RAIL_ITEM / 2)

/** Where the page starts: just past the resting pill, which floats over nothing. */
private val CONTENT_START = RAIL_INSET + RAIL_CLOSED + 8.dp

/** How dark the whole page goes while the rail is open, as a share of the rail's own wash. */
private const val PAGE_DIM = 0.7f

/** How far past the open rail its black wash fades to nothing. */
private val RAIL_FADE = 200.dp

@Composable
fun BrowseScreen() {
    val vm = graphViewModel { BrowseViewModel(it) }
    val railRequester = remember { FocusRequester() }
    var railHasFocus by remember { mutableStateOf(false) }
    var anyFocus by remember { mutableStateOf(false) }

    val libraries = (vm.libraries as? LoadState.Ready)?.value.orEmpty()
    val entries = buildList {
        add(RailEntry(HOME, "Home", Icons.Filled.Home))
        libraries.forEach {
            val icon = when (it.kind) {
                "show" -> AppIcons.Tv
                "music" -> AppIcons.Audio
                else -> AppIcons.Movie
            }
            add(RailEntry(librarySection(it.id), it.name, icon))
        }
        if (vm.liveTv) add(RailEntry(LIVE, "Live TV", AppIcons.Live))
        if (vm.podcasts) add(RailEntry(PODCASTS, "Podcasts", AppIcons.Podcast))
        add(RailEntry(SEARCH, "Search", Icons.Filled.Search))
        if (vm.isAdmin) add(RailEntry(ACTIVITY, "Activity", Icons.Filled.PlayArrow))
        add(RailEntry(SETTINGS, "Settings", Icons.Filled.Settings))
    }

    // Back walks outward: content to the rail, the rail to Home, Home's rail out of the app.
    BackHandler(enabled = !railHasFocus) { runCatching { railRequester.requestFocus() } }
    BackHandler(enabled = railHasFocus && vm.section != HOME) { vm.section = HOME }

    // The rail rests as a slim column of icons, so the content gets the screen.
    // With focus in it, it widens over the content (never pushing it, which would
    // lay out every row again) on a black wash, and the labels show.
    val railWidth by animateDpAsState(
        if (railHasFocus) RAIL_OPEN else RAIL_CLOSED,
        tween(Motion.NORMAL, easing = Motion.Ease),
        label = "rail width",
    )
    val railWash by animateFloatAsState(if (railHasFocus) 0.94f else 0f, tween(Motion.NORMAL, easing = Motion.Ease), label = "rail wash")
    Box(Modifier.fillMaxSize().onFocusChanged { anyFocus = it.hasFocus }) {
        Box(Modifier.fillMaxSize().padding(start = CONTENT_START).focusRestorer().focusGroup()) {
            val section = vm.section
            when {
                section == HOME -> HomeSection(vm.focus, onRetry = vm::loadLibraries)
                section == SEARCH -> SearchSection(vm.focus)
                section == SETTINGS -> SettingsSection(vm.focus)
                section == ACTIVITY -> ActivitySection(vm.focus)
                section == LIVE -> LiveSection(vm.focus)
                section == PODCASTS -> PodcastsSection(vm.focus)
                section.startsWith("library:") -> {
                    val library = libraries.firstOrNull { librarySection(it.id) == section }
                    when {
                        library != null && library.kind == "music" -> MusicSection(library, vm.focus)
                        library != null && library.kind == "show" -> ShowLibrarySection(library, vm.focus)
                        library != null -> LibrarySection(library, vm.focus)
                        vm.libraries is LoadState.Loading -> LoadingView()
                        else -> LaunchedEffect(section) { vm.section = HOME }
                    }
                }
            }
        }
        // With the rail open, the whole page dims, posters included, so the eye
        // goes to the rail (Pete, 2026-11-02: "the content on the right should dim
        // more"). Behind the rail itself the wash is darker still, and it fades out
        // past the rail so it never ends on a hard edge through the hero's text.
        if (railWash > 0f) {
            Box(Modifier.fillMaxSize().background(Black.copy(alpha = railWash * PAGE_DIM)))
            Box(
                Modifier
                    .width(RAIL_OPEN + RAIL_FADE)
                    .fillMaxHeight()
                    .background(
                        Brush.horizontalGradient(
                            0f to Black.copy(alpha = railWash),
                            RAIL_OPEN / (RAIL_OPEN + RAIL_FADE) to Black.copy(alpha = railWash * 0.85f),
                            1f to Color.Transparent,
                        ),
                    ),
            )
        }
        // The pill floats over the page: dark and a little see-through at rest, so
        // the hero's art shows behind it, near solid once it opens.
        val pillAlpha by animateFloatAsState(if (railHasFocus) 0.96f else 0.72f, tween(Motion.NORMAL, easing = Motion.Ease), label = "pill")
        Column(
            Modifier
                .align(Alignment.CenterStart)
                .padding(start = RAIL_INSET)
                .width(railWidth)
                .background(Surfaces.Raised.copy(alpha = pillAlpha), RAIL_SHAPE)
                .border(1.dp, Surfaces.Wash, RAIL_SHAPE)
                .padding(RAIL_PAD)
                .onFocusChanged { railHasFocus = it.hasFocus }
                .focusRestorer(railRequester)
                .focusGroup(),
            verticalArrangement = Arrangement.spacedBy(3.dp),
        ) {
            // No brand in the pill: anything shown only while it is open would
            // change its height, and a centred pill would then move every icon.
            entries.forEach { entry ->
                val selected = entry.key == vm.section
                RailItem(
                    entry = entry,
                    selected = selected,
                    open = railHasFocus,
                    onClick = {
                        vm.section = entry.key
                        vm.focus.toContent = true
                    },
                    modifier = if (selected) Modifier.focusRequester(railRequester) else Modifier,
                )
            }
        }
    }

    // If nothing took focus (a section failed silently, or Browse came back with its
    // card gone), give it to the rail so the D-pad always has somewhere to start.
    // Counted from when Browse is back in front, after the card has had its turn.
    val inFront = browseInFront()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(vm.section, inFront) {
        if (!inFront) return@LaunchedEffect
        delay(600)
        if (!anyFocus && lifecycle.inFrontNow()) runCatching { railRequester.requestFocus() }
    }
}

/**
 * One place in the rail. At rest a circle around the icon: the page you are on
 * reads white on a faint wash, the rest gray. Focused, the app's usual green
 * tint and ring. With the rail open the label shows beside the icon; closed,
 * TalkBack still reads it from the button.
 */
@Composable
private fun RailItem(entry: RailEntry, selected: Boolean, open: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Surface(
        onClick = onClick,
        modifier = modifier.fillMaxWidth().height(RAIL_ITEM).semantics { contentDescription = entry.label },
        shape = ClickableSurfaceDefaults.shape(RAIL_ITEM_SHAPE),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = if (selected) Surfaces.Wash else Color.Transparent,
            contentColor = if (selected) White else Gray,
            focusedContainerColor = AccentTint,
            focusedContentColor = White,
            pressedContainerColor = AccentTint,
            pressedContentColor = White,
        ),
        border = ClickableSurfaceDefaults.border(
            focusedBorder = Border(BorderStroke(FocusStroke, Accent), shape = RAIL_ITEM_SHAPE),
            pressedBorder = Border(BorderStroke(FocusStroke, Accent), shape = RAIL_ITEM_SHAPE),
        ),
        glow = ClickableSurfaceDefaults.glow(Glow.None, Glow.None, Glow.None),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1f),
    ) {
        Row(
            Modifier.fillMaxHeight().padding(start = (RAIL_ITEM - RAIL_ICON) / 2, end = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Icon(entry.icon, contentDescription = null, modifier = Modifier.size(RAIL_ICON))
            if (open) Label(entry.label, style = MaterialTheme.typography.labelLarge)
        }
    }
}

private val RAIL_ICON = 16.dp

/** The pill's padding around its buttons: (RAIL_CLOSED - RAIL_ITEM) / 2. */
private val RAIL_PAD = 5.dp
