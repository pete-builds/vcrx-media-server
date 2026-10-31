package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.BuildConfig
import io.github.pete_builds.vcrx.tv.R
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.AppIcons
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.settings.SettingRow
import io.github.pete_builds.vcrx.tv.ui.settings.SettingsPage
import io.github.pete_builds.vcrx.tv.ui.settings.pageHelp
import io.github.pete_builds.vcrx.tv.ui.settings.summary
import io.github.pete_builds.vcrx.tv.ui.theme.Gray

/**
 * Settings in the Browse rail: who is signed in, then one row per page with what
 * it is set to now. Subtitles come first: they are what people change most.
 */
@Composable
fun SettingsSection(focus: BrowseFocus) {
    val graph = LocalContext.current.graph
    val actions = LocalAppActions.current
    val prefs by graph.preferences.flow.collectAsState()
    val first = remember { FocusRequester() }
    val server = graph.settings.serverName ?: stringResource(R.string.server_product)
    val app = "${stringResource(R.string.app_title)} ${BuildConfig.VERSION_NAME}"

    Column(
        Modifier.padding(24.dp).width(640.dp).verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Column(Modifier.padding(start = 16.dp, bottom = 16.dp)) {
            Label("Settings", style = MaterialTheme.typography.labelMedium, color = Gray)
            Text("${graph.settings.username ?: "Unknown user"} on $server", style = MaterialTheme.typography.titleLarge)
        }
        SettingsPage.entries.forEach { page ->
            val value = when (page) {
                SettingsPage.Account -> graph.settings.username
                SettingsPage.About -> app
                else -> summary(page, prefs)
            }
            SettingRow(
                page.title,
                value,
                pageHelp(page),
                onClick = { actions.openSettings(page.name) },
                icon = icon(page),
                modifier = if (page == SettingsPage.entries.first()) Modifier.focusRequester(first) else Modifier,
            )
        }
    }
    LaunchedEffect(Unit) {
        if (focus.toContent) {
            runCatching { first.requestFocus() }
            focus.toContent = false
        }
    }
}

private fun icon(page: SettingsPage): ImageVector = when (page) {
    SettingsPage.Subtitles -> AppIcons.Subtitles
    SettingsPage.Playback -> Icons.Filled.PlayArrow
    SettingsPage.Audio -> AppIcons.Audio
    SettingsPage.Video -> AppIcons.Movie
    SettingsPage.Home -> Icons.Filled.Home
    SettingsPage.Account -> Icons.Filled.Person
    SettingsPage.About -> Icons.Filled.Info
}
