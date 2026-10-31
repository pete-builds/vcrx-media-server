package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Library
import io.github.pete_builds.vcrx.tv.ui.components.AppIcons
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadingView
import io.github.pete_builds.vcrx.tv.ui.components.Wordmark
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.music.MusicSection
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

const val HOME = "home"
const val SEARCH = "search"
const val SETTINGS = "settings"
const val ACTIVITY = "activity"
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
}

class BrowseViewModel(private val graph: AppGraph) : ViewModel() {
    var section by mutableStateOf(HOME)
    val focus = BrowseFocus()
    var libraries by mutableStateOf<LoadState<List<Library>>>(LoadState.Loading)
        private set

    /** Admins get Activity in the rail; the server enforces it either way. */
    var isAdmin by mutableStateOf(false)
        private set

    init {
        loadLibraries()
        viewModelScope.launch {
            isAdmin = try {
                graph.api.me().isAdmin
            } catch (_: ApiException) {
                false
            }
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

private data class RailEntry(val key: String, val label: String, val icon: ImageVector)

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
        add(RailEntry(SEARCH, "Search", Icons.Filled.Search))
        if (vm.isAdmin) add(RailEntry(ACTIVITY, "Activity", Icons.Filled.PlayArrow))
        add(RailEntry(SETTINGS, "Settings", Icons.Filled.Settings))
    }

    // Back walks outward: content to the rail, the rail to Home, Home's rail out of the app.
    BackHandler(enabled = !railHasFocus) { runCatching { railRequester.requestFocus() } }
    BackHandler(enabled = railHasFocus && vm.section != HOME) { vm.section = HOME }

    Row(Modifier.fillMaxSize().onFocusChanged { anyFocus = it.hasFocus }) {
        Column(
            Modifier
                .width(248.dp)
                .fillMaxHeight()
                .padding(start = 24.dp, top = 16.dp, end = 16.dp)
                .onFocusChanged { railHasFocus = it.hasFocus }
                .focusRestorer(railRequester)
                .focusGroup(),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Wordmark(height = 30.dp, modifier = Modifier.padding(start = 10.dp, top = 8.dp))
            Spacer(Modifier.height(20.dp))
            entries.forEach { entry ->
                val selected = entry.key == vm.section
                BrandRow(
                    onClick = {
                        vm.section = entry.key
                        vm.focus.toContent = true
                    },
                    selected = selected,
                    icon = entry.icon,
                    modifier = if (selected) Modifier.focusRequester(railRequester) else Modifier,
                ) {
                    Label(entry.label, style = MaterialTheme.typography.labelLarge)
                }
            }
        }
        Box(Modifier.weight(1f).fillMaxHeight().focusRestorer().focusGroup()) {
            val section = vm.section
            when {
                section == HOME -> HomeSection(vm.focus, onRetry = vm::loadLibraries)
                section == SEARCH -> SearchSection(vm.focus)
                section == SETTINGS -> SettingsSection(vm.focus)
                section == ACTIVITY -> ActivitySection(vm.focus)
                section.startsWith("library:") -> {
                    val library = libraries.firstOrNull { librarySection(it.id) == section }
                    when {
                        library != null && library.kind == "music" -> MusicSection(library, vm.focus)
                        library != null -> LibrarySection(library, vm.focus)
                        vm.libraries is LoadState.Loading -> LoadingView()
                        else -> LaunchedEffect(section) { vm.section = HOME }
                    }
                }
            }
        }
    }

    // If nothing took focus (a section failed silently, or Browse came back with its
    // card gone), give it to the rail so the D-pad always has somewhere to start.
    LaunchedEffect(vm.section) {
        delay(600)
        if (!anyFocus) runCatching { railRequester.requestFocus() }
    }
}
