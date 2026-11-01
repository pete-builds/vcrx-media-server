package io.github.pete_builds.vcrx.tv.ui

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.MusicPlayer
import io.github.pete_builds.vcrx.tv.ui.browse.BrowseScreen
import io.github.pete_builds.vcrx.tv.ui.connect.ConnectScreen
import io.github.pete_builds.vcrx.tv.ui.connect.LoginScreen
import io.github.pete_builds.vcrx.tv.ui.collection.CollectionScreen
import io.github.pete_builds.vcrx.tv.ui.details.DetailsScreen
import io.github.pete_builds.vcrx.tv.ui.music.LocalMusicNav
import io.github.pete_builds.vcrx.tv.ui.music.MusicNav
import io.github.pete_builds.vcrx.tv.ui.music.musicScreens
import io.github.pete_builds.vcrx.tv.ui.pair.PairScreen
import io.github.pete_builds.vcrx.tv.ui.profiles.ProfilePickerScreen
import io.github.pete_builds.vcrx.tv.ui.rewind.RewindScreen
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.map
import io.github.pete_builds.vcrx.tv.ui.profiles.StartScreen
import io.github.pete_builds.vcrx.tv.ui.profiles.startScreen
import io.github.pete_builds.vcrx.tv.ui.settings.AboutScreen
import io.github.pete_builds.vcrx.tv.ui.settings.SettingsScreen
import io.github.pete_builds.vcrx.tv.ui.theme.BrandTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val graph = graph
        val start: Any = when (
            startScreen(graph.settings.serverUrl != null, graph.tokens.token() != null, graph.profilePrefs.alwaysAsk, graph.tokens.isInvitedDevice)
        ) {
            StartScreen.CONNECT -> ConnectRoute
            StartScreen.LOGIN -> LoginRoute
            StartScreen.PICKER -> ProfilesRoute
            StartScreen.BROWSE -> BrowseRoute
        }
        setContent {
            BrandTheme {
                val nav = rememberNavController()
                val actions = remember(nav) { AppActions(nav, this) }
                // Any 401 anywhere lands here and returns to Login with a clean back stack.
                LaunchedEffect(nav) { graph.signedOut.collect { actions.toLogin() } }
                // A lent tape that came back: "Be kind, rewind." rather than Login.
                LaunchedEffect(nav) { graph.rewind.filterNotNull().collect { actions.toRewind() } }
                val musicNav = remember(nav) { MusicNav(nav) }
                CompositionLocalProvider(LocalAppActions provides actions, LocalMusicNav provides musicNav) {
                    NavHost(navController = nav, startDestination = start) {
                        composable<ConnectRoute> { ConnectScreen() }
                        composable<LoginRoute> { LoginScreen() }
                        composable<BrowseRoute> { BrowseScreen() }
                        composable<DetailsRoute> { entry -> DetailsScreen(entry.toRoute<DetailsRoute>().itemId) }
                        composable<CollectionRoute> { entry ->
                            val route = entry.toRoute<CollectionRoute>()
                            CollectionScreen(route.collectionId, route.name)
                        }
                        composable<AboutRoute> { AboutScreen() }
                        composable<ProfilesRoute> { ProfilePickerScreen() }
                        composable<PairRoute> { PairScreen() }
                        composable<SettingsRoute> { entry -> SettingsScreen(entry.toRoute<SettingsRoute>().page) }
                        composable<RewindRoute> {
                            val rewound by graph.rewind.collectAsState()
                            RewindScreen(rewound)
                        }
                        musicScreens()
                    }
                }
            }
        }
    }

    override fun onStop() {
        super.onStop()
        // Home, another app, or a video taking the screen: the music stops with
        // its listen rather than playing on with nothing on screen to stop it.
        if (!isChangingConfigurations) MusicPlayer.stopIfRunning()
    }
}

/**
 * Keeps the screen on while music plays, so the screensaver (which stops this
 * activity, and with it the music) does not cut an album off. Only a change
 * between playing and not playing recomposes this, not every position tick.
 */
@Composable
private fun KeepScreenOnWhileMusicPlays() {
    val context = LocalContext.current
    val playing by remember {
        MusicPlayer.get(context.graph, context).state.map { it.playing }.distinctUntilChanged()
    }.collectAsState(initial = false)
    val view = LocalView.current
    DisposableEffect(view, playing) {
        view.keepScreenOn = playing
        onDispose { view.keepScreenOn = false }
    }
}
