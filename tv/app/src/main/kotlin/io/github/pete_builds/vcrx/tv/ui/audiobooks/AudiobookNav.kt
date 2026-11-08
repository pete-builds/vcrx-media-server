package io.github.pete_builds.vcrx.tv.ui.audiobooks

import androidx.compose.runtime.staticCompositionLocalOf
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import kotlinx.serialization.Serializable

@Serializable
data class AudiobookRoute(val bookId: Long)

@Serializable
object AudiobookPlayingRoute

/** Navigation between the audiobook screens, kept apart from AppActions as music's is. */
class AudiobookNav(private val nav: NavController) {
    fun openBook(bookId: Long) = nav.navigate(AudiobookRoute(bookId))

    /** The book player once on the stack: opening it again returns to the same screen. */
    fun openPlaying() = nav.navigate(AudiobookPlayingRoute) { launchSingleTop = true }
}

val LocalAudiobookNav = staticCompositionLocalOf<AudiobookNav> { error("AudiobookNav not provided") }

/** The audiobook screens, added to the app's NavHost. */
fun NavGraphBuilder.audiobookScreens() {
    composable<AudiobookRoute> { entry -> AudiobookScreen(entry.toRoute<AudiobookRoute>().bookId) }
    composable<AudiobookPlayingRoute> { AudiobookPlayingScreen() }
}
