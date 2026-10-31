package io.github.pete_builds.vcrx.tv

import android.content.Context
import android.content.res.Resources
import android.os.Build
import io.github.pete_builds.vcrx.tv.data.PlaybackEventSender
import io.github.pete_builds.vcrx.tv.data.PreferencesStore
import io.github.pete_builds.vcrx.tv.data.ProfilePrefs
import io.github.pete_builds.vcrx.tv.data.SettingsStore
import io.github.pete_builds.vcrx.tv.data.TokenStore
import io.github.pete_builds.vcrx.tv.net.AuthInterceptor
import io.github.pete_builds.vcrx.tv.net.ServerApi
import io.github.pete_builds.vcrx.tv.net.ApiJson
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient

/** The app's singletons, built once by [TvApplication]. No DI framework: there are few of them. */
class AppGraph(context: Context) {
    /** Outlives activities; used for event delivery and other work that must finish. */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** For the few messages built outside Compose; Compose code uses stringResource. */
    val resources: Resources = context.resources

    val settings = SettingsStore(context)
    val tokens = TokenStore(context)
    val profilePrefs = ProfilePrefs(context)

    /** Settings and remembered subtitle choices, kept per server and user. */
    val preferences = PreferencesStore(context, settings)

    val json: Json = ApiJson

    /** One client for API calls, artwork, streams and subtitles, so all share the auth header and the pool. */
    val httpClient: OkHttpClient = OkHttpClient.Builder()
        .addInterceptor(
            AuthInterceptor(
                settings,
                tokens,
                "${context.getString(R.string.user_agent_product)}/${BuildConfig.VERSION_NAME} " +
                    "(Android ${Build.VERSION.RELEASE}; ${Build.MANUFACTURER} ${Build.MODEL})",
            ),
        )
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(30, TimeUnit.SECONDS)
        .build()

    private val signedOutEvents = MutableSharedFlow<Unit>(extraBufferCapacity = 1)

    /** Emits when any request gets a 401; the UI returns to the Login screen. */
    val signedOut: SharedFlow<Unit> = signedOutEvents.asSharedFlow()

    val api = ServerApi(httpClient, { settings.serverUrl }, json) {
        tokens.clear()
        signedOutEvents.tryEmit(Unit)
    }

    val playbackEvents = PlaybackEventSender(context, api, json, appScope)
}
