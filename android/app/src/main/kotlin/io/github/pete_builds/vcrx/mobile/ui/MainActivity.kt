package io.github.pete_builds.vcrx.mobile.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.NavigationRail
import androidx.compose.material3.NavigationRailItem
import androidx.compose.material3.NavigationRailItemDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.graphics.vector.rememberVectorPainter
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.core.content.ContextCompat
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import io.github.pete_builds.vcrx.mobile.R
import io.github.pete_builds.vcrx.mobile.auto.Features
import io.github.pete_builds.vcrx.mobile.graph

class MainActivity : ComponentActivity() {
    private val openRequest = mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        openRequest.value = intent?.getStringExtra(EXTRA_OPEN)
        setContent { VcrxTheme { App(openRequest.value) { openRequest.value = null } } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        openRequest.value = intent.getStringExtra(EXTRA_OPEN)
    }

    companion object {
        const val EXTRA_OPEN = "open"
        const val OPEN_DOWNLOADS = "downloads"
    }
}

private object Routes {
    const val CONNECT = "connect"
    const val LOGIN = "login"
    const val PROFILES = "profiles"
    const val INVITE = "invite"
    const val HOME = "home"
    const val SEARCH = "search"
    const val LIBRARIES = "libraries"
    const val MUSIC = "music"
    const val IPTV = "iptv"
    const val AUDIOBOOKS = "audiobooks"
    const val PODCASTS = "podcasts"
    const val PODCAST_ADD = "podcasts/add"
    const val DOWNLOADS = "downloads"
    const val SETTINGS = "settings"
    const val NOW_PLAYING = "now"
    val AUTH = setOf(CONNECT, LOGIN, PROFILES, INVITE)
}

private data class Tab(val route: String, val label: String, val icon: @Composable () -> Painter)

private fun NavHostController.reset(route: String) = navigate(route) {
    popUpTo(0) { inclusive = true }
    launchSingleTop = true
}

private fun NavHostController.tab(route: String) = navigate(route) {
    popUpTo(Routes.HOME) { saveState = true }
    launchSingleTop = true
    restoreState = true
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun App(openRequest: String?, consumed: () -> Unit) {
    val context = LocalContext.current
    val graph = context.graph
    val nav = rememberNavController()
    val start = remember {
        when {
            graph.settings.serverUrl == null -> Routes.CONNECT
            graph.tokens.token() == null -> Routes.LOGIN
            else -> Routes.HOME
        }
    }
    val me by graph.me.collectAsState()
    val online by graph.online.collectAsState()
    val libraries by graph.libraries.collectAsState()
    val entry by nav.currentBackStackEntryAsState()
    val route = entry?.destination?.route
    val signedIn = route != null && route !in Routes.AUTH
    val remote = if (signedIn) rememberMusicRemote() else null
    val (now, _) = rememberNowPlaying(remote)

    val askNotifications = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    var asked by remember { mutableStateOf(false) }
    LaunchedEffect(signedIn) {
        if (!signedIn) return@LaunchedEffect
        if (Build.VERSION.SDK_INT >= 33 && !asked &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            asked = true
            askNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        // Offline start: no answer from the server opens Downloads, which needs none.
        graph.refresh()
        if (!graph.online.value && route != Routes.DOWNLOADS) nav.tab(Routes.DOWNLOADS)
    }
    LaunchedEffect(Unit) { graph.signedOut.collect { nav.reset(Routes.LOGIN) } }
    LaunchedEffect(openRequest, signedIn) {
        if (openRequest == MainActivity.OPEN_DOWNLOADS && signedIn) {
            nav.tab(Routes.DOWNLOADS)
            consumed()
        }
    }

    // Audiobooks and podcasts show only when the server reports them: an
    // audiobook library in /libraries, podcasts in /me (never an invited device).
    val features = Features.of(libraries, me, graph.tokens.isInvitedDevice)
    val tabs = buildList {
        add(Tab(Routes.HOME, "Home") { rememberVectorPainter(Icons.Filled.Home) })
        add(Tab(Routes.SEARCH, "Search") { rememberVectorPainter(Icons.Filled.Search) })
        add(Tab(Routes.LIBRARIES, "Library") { painterResource(R.drawable.ic_library) })
        if (features.music) add(Tab(Routes.MUSIC, "Music") { painterResource(R.drawable.ic_music) })
        if (features.audiobooks) add(Tab(Routes.AUDIOBOOKS, "Audiobooks") { painterResource(R.drawable.ic_book) })
        if (features.podcasts) add(Tab(Routes.PODCASTS, "Podcasts") { painterResource(R.drawable.ic_podcast) })
        if (me?.liveTv == true && online) add(Tab(Routes.IPTV, "IPTV") { painterResource(R.drawable.ic_iptv) })
        add(Tab(Routes.DOWNLOADS, "Downloads") { painterResource(R.drawable.ic_download) })
    }
    val topLevel = tabs.any { it.route == route }
    val wide = LocalConfiguration.current.screenWidthDp >= 600
    val (barRoutes, moreRoutes) = NavTabs.split(tabs.map { it.route })
    var moreOpen by remember { mutableStateOf(false) }
    val openPlaying = { nav.navigate(Routes.NOW_PLAYING) }

    Scaffold(
        topBar = {
            if (signedIn) {
                TopAppBar(
                    title = { Text(titleFor(route, tabs)) },
                    navigationIcon = {
                        if (!topLevel) IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
                    },
                    actions = {
                        if (route != Routes.SETTINGS) IconButton(onClick = { nav.navigate(Routes.SETTINGS) }) { Icon(Icons.Filled.Settings, "Settings") }
                    },
                    colors = TopAppBarDefaults.topAppBarColors(containerColor = Brand.Black),
                )
            }
        },
        bottomBar = {
            if (signedIn) {
                Column {
                    if (remote != null && now != null && route != Routes.NOW_PLAYING) MiniPlayer(remote, now) { nav.navigate(Routes.NOW_PLAYING) }
                    if (!wide) {
                        NavigationBar(containerColor = Brand.Black) {
                            barRoutes.forEach { r ->
                                val t = tabs.firstOrNull { it.route == r }
                                if (t != null) {
                                    NavigationBarItem(
                                        selected = route == t.route,
                                        onClick = { nav.tab(t.route) },
                                        icon = { Icon(t.icon(), null) },
                                        label = { Text(t.label) },
                                        colors = NavigationBarItemDefaults.colors(indicatorColor = Brand.Tint, selectedIconColor = Brand.Green),
                                    )
                                } else {
                                    // More: the destinations a phone's bar has no room for.
                                    NavigationBarItem(
                                        selected = route in moreRoutes,
                                        onClick = { moreOpen = true },
                                        icon = {
                                            Box {
                                                Icon(Icons.Filled.MoreVert, null)
                                                DropdownMenu(expanded = moreOpen, onDismissRequest = { moreOpen = false }) {
                                                    tabs.filter { it.route in moreRoutes }.forEach { m ->
                                                        DropdownMenuItem(
                                                            text = { Text(m.label) },
                                                            leadingIcon = { Icon(m.icon(), null) },
                                                            onClick = {
                                                                moreOpen = false
                                                                nav.tab(m.route)
                                                            },
                                                        )
                                                    }
                                                }
                                            }
                                        },
                                        label = { Text("More") },
                                        colors = NavigationBarItemDefaults.colors(indicatorColor = Brand.Tint, selectedIconColor = Brand.Green),
                                    )
                                }
                            }
                        }
                    }
                }
            }
        },
        containerColor = Brand.Black,
    ) { padding ->
        Row(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding)) {
            if (signedIn && wide) {
                NavigationRail(containerColor = Brand.Black) {
                    tabs.forEach { t ->
                        NavigationRailItem(
                            selected = route == t.route,
                            onClick = { nav.tab(t.route) },
                            icon = { Icon(t.icon(), null) },
                            label = { Text(t.label) },
                            colors = NavigationRailItemDefaults.colors(indicatorColor = Brand.Tint, selectedIconColor = Brand.Green),
                        )
                    }
                }
            }
            NavHost(nav, startDestination = start, modifier = Modifier.weight(1f)) {
                composable(Routes.CONNECT) {
                    ConnectScreen(
                        onConnected = { picker -> nav.navigate(if (picker) Routes.PROFILES else Routes.LOGIN) },
                        onInvite = { nav.navigate(Routes.INVITE) },
                    )
                }
                composable(Routes.LOGIN) {
                    LoginScreen(
                        onSignedIn = { nav.reset(Routes.HOME) },
                        onProfiles = { nav.navigate(Routes.PROFILES) },
                        onChangeServer = { nav.reset(Routes.CONNECT) },
                    )
                }
                composable(Routes.PROFILES) { ProfilesScreen(onSignedIn = { nav.reset(Routes.HOME) }, onPassword = { nav.navigate(Routes.LOGIN) }) }
                composable(Routes.INVITE) { InviteScreen(onSignedIn = { nav.reset(Routes.HOME) }, onBack = { nav.popBackStack() }) }
                composable(Routes.HOME) { HomeScreen(onItem = { nav.navigate("item/$it") }) }
                composable(Routes.SEARCH) { SearchScreen(onItem = { nav.navigate("item/$it") }) }
                composable(Routes.LIBRARIES) {
                    var other by remember { mutableStateOf<String?>(null) }
                    LibrariesScreen(
                        onLibrary = { id, _ -> nav.navigate("library/$id") },
                        onMusic = { nav.tab(Routes.MUSIC) },
                        onAudiobooks = { nav.tab(Routes.AUDIOBOOKS) },
                        onPodcasts = if (features.podcasts) ({ nav.tab(Routes.PODCASTS) }) else null,
                        onOther = { other = it },
                    )
                    other?.let { name ->
                        androidx.compose.material3.AlertDialog(
                            onDismissRequest = { other = null },
                            title = { Text(name) },
                            text = { Text("This kind of library needs a newer version of the app.") },
                            confirmButton = { QuietButton("OK", onClick = { other = null }) },
                        )
                    }
                }
                composable(Routes.MUSIC) { MusicScreen(onAlbum = { nav.navigate("album/$it") }, onArtist = { nav.navigate("artist/$it") }) }
                composable(Routes.IPTV) { IptvScreen() }
                composable(Routes.AUDIOBOOKS) { AudiobooksScreen(onBook = { nav.navigate("book/$it") }) }
                composable(Routes.PODCASTS) {
                    PodcastsScreen(onShow = { nav.navigate("show/$it") }, onAdd = { nav.navigate(Routes.PODCAST_ADD) }, remote = remote, onPlaying = openPlaying)
                }
                composable(Routes.PODCAST_ADD) {
                    AddPodcastScreen(onAdded = { id ->
                        nav.popBackStack()
                        nav.navigate("show/$id")
                    })
                }
                composable("book/{id}", listOf(navArgument("id") { type = NavType.LongType })) {
                    BookScreen(it.arguments?.getLong("id") ?: 0, remote, onPlaying = openPlaying)
                }
                composable("show/{id}", listOf(navArgument("id") { type = NavType.LongType })) {
                    ShowScreen(it.arguments?.getLong("id") ?: 0, remote, onPlaying = openPlaying, onGone = { nav.popBackStack() })
                }
                composable(Routes.DOWNLOADS) { DownloadsScreen(remote) }
                composable(Routes.SETTINGS) { SettingsScreen(onSwitchProfile = { nav.navigate(Routes.PROFILES) }, onSignedOut = { nav.reset(Routes.LOGIN) }) }
                composable(Routes.NOW_PLAYING) { NowPlayingScreen(remote) }
                composable("library/{id}", listOf(navArgument("id") { type = NavType.LongType })) {
                    LibraryScreen(it.arguments?.getLong("id") ?: 0, onItem = { id -> nav.navigate("item/$id") })
                }
                composable("item/{id}", listOf(navArgument("id") { type = NavType.LongType })) {
                    DetailsScreen(it.arguments?.getLong("id") ?: 0, onItem = { id -> nav.navigate("item/$id") })
                }
                composable("album/{id}", listOf(navArgument("id") { type = NavType.LongType })) {
                    AlbumScreen(it.arguments?.getLong("id") ?: 0, remote, onArtist = { id -> nav.navigate("artist/$id") })
                }
                composable("artist/{id}", listOf(navArgument("id") { type = NavType.LongType })) {
                    ArtistScreen(it.arguments?.getLong("id") ?: 0, remote, onAlbum = { id -> nav.navigate("album/$id") })
                }
            }
        }
    }
}

private fun titleFor(route: String?, tabs: List<Tab>): String = when (route) {
    Routes.SETTINGS -> "Settings"
    Routes.NOW_PLAYING -> "Now playing"
    Routes.PODCAST_ADD -> "Add a podcast"
    else -> tabs.firstOrNull { it.route == route }?.label ?: ""
}
