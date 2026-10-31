package io.github.pete_builds.vcrx.tv.ui.profiles

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label

/**
 * The "Switch profile" entry point for any menu: a row that opens "Who's
 * watching?" on top of the current screen, so Back returns to it. Settings shows
 * it; a rail or top bar can place the same row, or call
 * LocalAppActions.current.openProfiles() from its own control.
 */
@Composable
fun SwitchProfileRow(modifier: Modifier = Modifier) {
    val actions = LocalAppActions.current
    BrandRow(onClick = { actions.openProfiles() }, icon = Icons.Filled.AccountCircle, modifier = modifier) {
        Label("Switch profile")
    }
}
