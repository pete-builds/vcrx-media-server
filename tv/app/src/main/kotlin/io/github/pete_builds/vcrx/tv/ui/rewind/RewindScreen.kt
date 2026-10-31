package io.github.pete_builds.vcrx.tv.ui.rewind

import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalContext
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.MessageView

/**
 * "Be kind, rewind.": the borrowed tape is back on its owner's shelf. OK goes
 * to sign-in, where another card's code can be typed.
 */
@Composable
fun RewindScreen(state: RewindState?) {
    val actions = LocalAppActions.current
    val graph = LocalContext.current.graph
    MessageView(
        title = RewindLogic.HEADLINE,
        detail = state?.detail ?: RewindLogic.FALLBACK,
        primary = "OK" to {
            graph.clearRewind()
            actions.toLogin()
        },
    )
}
