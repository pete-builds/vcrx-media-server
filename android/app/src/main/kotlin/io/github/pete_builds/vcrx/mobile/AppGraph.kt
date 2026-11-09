package io.github.pete_builds.vcrx.mobile

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.os.Build
import io.github.pete_builds.vcrx.mobile.auto.Features
import io.github.pete_builds.vcrx.mobile.caps.CapabilityProbe
import io.github.pete_builds.vcrx.mobile.data.PhonePrefs
import io.github.pete_builds.vcrx.mobile.data.PlaybackEventSender
import io.github.pete_builds.vcrx.mobile.data.SettingsStore
import io.github.pete_builds.vcrx.mobile.data.TokenStore
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.ApiJson
import io.github.pete_builds.vcrx.mobile.net.AuthInterceptor
import io.github.pete_builds.vcrx.mobile.net.Capabilities
import io.github.pete_builds.vcrx.mobile.net.Library
import io.github.pete_builds.vcrx.mobile.net.ServerApi
import io.github.pete_builds.vcrx.mobile.net.User
import io.github.pete_builds.vcrx.mobile.offline.DownloadEligibility
import io.github.pete_builds.vcrx.mobile.offline.Downloads
import io.github.pete_builds.vcrx.mobile.offline.OfflineStore
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient

/** The app's singletons, built once by [VcrxApp]. No DI framework: there are few of them. */
class AppGraph(private val context: Context) {
    /** Outlives activities; for event delivery and syncs that must finish. */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.Main)

    val settings = SettingsStore(context)
    val tokens = TokenStore(context)
    val prefs = PhonePrefs(context)

    /** One client for API calls, artwork, streams, subtitles and downloads, so all share the auth header. */
    val httpClient: OkHttpClient = OkHttpClient.Builder()
        .addInterceptor(
            AuthInterceptor(
                settings,
                tokens,
                "VCRX-Android/${BuildConfig.VERSION_NAME} (Android ${Build.VERSION.RELEASE}; ${Build.MANUFACTURER} ${Build.MODEL})",
            ),
        )
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(30, TimeUnit.SECONDS)
        .build()

    private val signedOutEvents = MutableSharedFlow<Unit>(extraBufferCapacity = 1)

    /** Emits when a request gets a 401; the UI returns to sign in. */
    val signedOut: SharedFlow<Unit> = signedOutEvents.asSharedFlow()

    val api = ServerApi(httpClient, { settings.serverUrl }, ApiJson, tokens::token) {
        tokens.clear()
        signedOutEvents.tryEmit(Unit)
    }

    val events = PlaybackEventSender(context, api, ApiJson, appScope)

    private val onlineState = MutableStateFlow(true)

    /** Whether the server answered the last time the app asked. Offline, the app opens to Downloads. */
    val online: StateFlow<Boolean> = onlineState.asStateFlow()

    private val meState = MutableStateFlow(prefs.cachedMe())

    /** Who this session is, kept on disk so an offline start still knows. */
    val me: StateFlow<User?> = meState.asStateFlow()

    private val libraryState = MutableStateFlow(prefs.cachedLibraries())
    val libraries: StateFlow<List<Library>> = libraryState.asStateFlow()

    val offline = OfflineStore(context)

    val downloads: Downloads by lazy { Downloads(context, this) }

    /** What this phone decodes, probed once. The server picks direct play or a converted stream from it. */
    val capabilities: Capabilities by lazy { CapabilityProbe.probe(context) }

    fun canDownload(): Boolean = DownloadEligibility.allowed(me.value, tokens.isInvitedDevice)

    /** What the server offers this session, for the bottom bar and Android Auto's root. */
    fun features(): Features = Features(
        music = libraries.value.any { it.kind == "music" },
        // Audiobooks and podcasts stay off until the phone has their screens and
        // Auto nodes, which follow once those server APIs are on main: an empty tab
        // in the car is worse than none. The browse tree already places them
        // (BrowseTreeTest); the follow-up sets these from the library kinds.
        audiobooks = false,
        podcasts = false,
        hasDownloads = true,
    )

    /**
     * Asks the server who this is. Unreachable marks the app offline; any answer
     * marks it online and, signed in, sends progress kept while offline.
     */
    suspend fun refresh(): Boolean {
        if (settings.serverUrl == null || tokens.token() == null) return false
        return try {
            val user = api.me()
            meState.value = user
            prefs.saveMe(user)
            onlineState.value = true
            runCatching { api.libraries() }.getOrNull()?.let {
                libraryState.value = it
                prefs.saveLibraries(it)
            }
            offline.sync(api, capabilities)
            true
        } catch (e: ApiException.Unreachable) {
            onlineState.value = false
            false
        } catch (e: ApiException) {
            onlineState.value = e !is ApiException.Unreachable
            false
        }
    }

    fun setSignedIn(user: User) {
        meState.value = user
        prefs.saveMe(user)
        onlineState.value = true
        appScope.launch { refresh() }
    }

    fun forgetSession() {
        meState.value = null
        prefs.saveMe(null)
    }

    /** Re-checks the server whenever the phone gets a network back, which also syncs offline progress. */
    fun watchNetwork() {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return
        runCatching {
            cm.registerDefaultNetworkCallback(
                object : ConnectivityManager.NetworkCallback() {
                    override fun onAvailable(network: Network) {
                        appScope.launch { refresh() }
                    }

                    override fun onLost(network: Network) {
                        appScope.launch { refresh() }
                    }
                },
            )
        }
    }
}
