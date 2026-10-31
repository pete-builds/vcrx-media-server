package io.github.pete_builds.vcrx.tv.ui.music

import androidx.compose.runtime.staticCompositionLocalOf
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import kotlinx.serialization.Serializable

@Serializable
data class MusicAlbumRoute(val albumId: Long)

@Serializable
data class MusicArtistRoute(val artistId: Long)

@Serializable
object NowPlayingRoute

/** Navigation between the music screens, kept apart from AppActions. */
class MusicNav(private val nav: NavController) {
    fun openAlbum(albumId: Long) = nav.navigate(MusicAlbumRoute(albumId))

    fun openArtist(artistId: Long) = nav.navigate(MusicArtistRoute(artistId))

    /** Now Playing once on the stack: opening it again returns to the same screen. */
    fun openNowPlaying() = nav.navigate(NowPlayingRoute) { launchSingleTop = true }
}

val LocalMusicNav = staticCompositionLocalOf<MusicNav> { error("MusicNav not provided") }

/** The music screens, added to the app's NavHost. */
fun NavGraphBuilder.musicScreens() {
    composable<MusicAlbumRoute> { entry -> MusicAlbumScreen(entry.toRoute<MusicAlbumRoute>().albumId) }
    composable<MusicArtistRoute> { entry -> MusicArtistScreen(entry.toRoute<MusicArtistRoute>().artistId) }
    composable<NowPlayingRoute> { NowPlayingScreen() }
}
