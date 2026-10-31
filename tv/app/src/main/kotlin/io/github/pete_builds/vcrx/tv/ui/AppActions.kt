package io.github.pete_builds.vcrx.tv.ui

import android.app.Activity
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.navigation.NavController
import io.github.pete_builds.vcrx.tv.player.BeforeMovie
import io.github.pete_builds.vcrx.tv.player.BeforeMovieActivity
import io.github.pete_builds.vcrx.tv.player.PlayerActivity
import io.github.pete_builds.vcrx.tv.player.StartRequest
import kotlinx.serialization.Serializable

@Serializable
object ConnectRoute

@Serializable
object LoginRoute

@Serializable
object BrowseRoute

@Serializable
data class DetailsRoute(val itemId: Long)

@Serializable
data class CollectionRoute(val collectionId: Long, val name: String)

@Serializable
object AboutRoute

/** "Who's watching?", the household profile picker. */
@Serializable
object ProfilesRoute

/** Pair with a phone, or type an invite: signs in as an invited device. */
@Serializable
object PairRoute
/** One page of Settings; [page] is a [io.github.pete_builds.vcrx.tv.ui.settings.SettingsPage] name. */
@Serializable
data class SettingsRoute(val page: String)

/** "Be kind, rewind.": a lent tape's loan is over. */
@Serializable
object RewindRoute

/** Navigation any screen may need, without threading callbacks through every layer. */
class AppActions(private val nav: NavController, private val activity: Activity) {
    /** Connect on top of the current screen; Back returns to it. */
    fun openConnect() = nav.navigate(ConnectRoute) { launchSingleTop = true }

    /** Connect as the only screen, so Back from it leaves the app. */
    fun toConnect() = nav.navigate(ConnectRoute) {
        popUpTo(nav.graph.id) { inclusive = true }
        launchSingleTop = true
    }

    fun openDetails(itemId: Long) = nav.navigate(DetailsRoute(itemId))

    fun openCollection(collectionId: Long, name: String) = nav.navigate(CollectionRoute(collectionId, name))

    fun openAbout() = nav.navigate(AboutRoute) { launchSingleTop = true }

    fun openSettings(page: String) = nav.navigate(SettingsRoute(page)) { launchSingleTop = true }

    /**
     * Starts playback. [fromStart] ignores the saved position and [resume] uses it (the
     * explicit buttons); a plain Play leaves it to Settings > Playback > Resume.
     */
    fun play(itemId: Long, title: String, versionId: Long? = null, fromStart: Boolean = false, resume: Boolean = false) {
        val start = when {
            fromStart -> StartRequest.Beginning
            resume -> StartRequest.Resume
            else -> StartRequest.Default
        }
        val player = PlayerActivity.intent(activity, itemId, versionId, start, title)
        // From the beginning: the profile's Before a movie clips first, if any.
        activity.startActivity(if (BeforeMovie.asks(start, null)) BeforeMovieActivity.wrap(activity, player) else player)
    }

    /** The profile picker on top of the current screen; Back returns to it. */
    fun openProfiles() = nav.navigate(ProfilesRoute) { launchSingleTop = true }

    /** The profile picker as the only screen: after connecting, before anyone is signed in. */
    fun toProfiles() = nav.navigate(ProfilesRoute) {
        popUpTo(nav.graph.id) { inclusive = true }
        launchSingleTop = true
    }

    /** Back out of the picker: to the screen under it, or out of the app when it is the only one. */
    fun leaveProfiles() {
        if (!nav.popBackStack()) activity.finish()
    }

    /** Pairing on top of Connect or Login; Back returns to it. */
    fun openPair() = nav.navigate(PairRoute) { launchSingleTop = true }

    /** Plays one of an item's extras, such as its trailer, in the app's player. */
    fun playExtra(itemId: Long, extraId: Long, title: String) {
        activity.startActivity(PlayerActivity.extraIntent(activity, itemId, extraId, title))
    }

    fun toLogin() = nav.navigate(LoginRoute) {
        popUpTo(nav.graph.id) { inclusive = true }
        launchSingleTop = true
    }

    /** "Be kind, rewind." as the only screen. */
    fun toRewind() = nav.navigate(RewindRoute) {
        popUpTo(nav.graph.id) { inclusive = true }
        launchSingleTop = true
    }

    fun toBrowse() = nav.navigate(BrowseRoute) {
        popUpTo(nav.graph.id) { inclusive = true }
        launchSingleTop = true
    }
}

val LocalAppActions = staticCompositionLocalOf<AppActions> { error("AppActions not provided") }
