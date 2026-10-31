package io.github.pete_builds.vcrx.tv.ui

import android.app.Activity
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.navigation.NavController
import io.github.pete_builds.vcrx.tv.player.PlayerActivity
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

    /** Starts playback. [fromStart] ignores the saved position; otherwise the server resumes it. */
    fun play(itemId: Long, title: String, versionId: Long? = null, fromStart: Boolean = false) {
        activity.startActivity(PlayerActivity.intent(activity, itemId, versionId, fromStart, title))
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

    fun toLogin() = nav.navigate(LoginRoute) {
        popUpTo(nav.graph.id) { inclusive = true }
        launchSingleTop = true
    }

    fun toBrowse() = nav.navigate(BrowseRoute) {
        popUpTo(nav.graph.id) { inclusive = true }
        launchSingleTop = true
    }
}

val LocalAppActions = staticCompositionLocalOf<AppActions> { error("AppActions not provided") }
