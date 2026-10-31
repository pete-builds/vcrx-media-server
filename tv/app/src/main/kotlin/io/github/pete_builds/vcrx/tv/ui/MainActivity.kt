package io.github.pete_builds.vcrx.tv.ui

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.ui.browse.BrowseScreen
import io.github.pete_builds.vcrx.tv.ui.connect.ConnectScreen
import io.github.pete_builds.vcrx.tv.ui.connect.LoginScreen
import io.github.pete_builds.vcrx.tv.ui.collection.CollectionScreen
import io.github.pete_builds.vcrx.tv.ui.details.DetailsScreen
import io.github.pete_builds.vcrx.tv.ui.pair.PairScreen
import io.github.pete_builds.vcrx.tv.ui.profiles.ProfilePickerScreen
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
                CompositionLocalProvider(LocalAppActions provides actions) {
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
                    }
                }
            }
        }
    }
}
