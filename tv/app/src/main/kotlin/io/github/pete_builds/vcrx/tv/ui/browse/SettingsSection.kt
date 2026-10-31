package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import androidx.compose.ui.res.stringResource
import io.github.pete_builds.vcrx.tv.BuildConfig
import io.github.pete_builds.vcrx.tv.R
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.profiles.SwitchProfileRow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

@Composable
fun SettingsSection(focus: BrowseFocus) {
    val graph = LocalContext.current.graph
    val actions = LocalAppActions.current
    val scope = rememberCoroutineScope()
    val first = remember { FocusRequester() }
    var signingOut by remember { mutableStateOf(false) }

    Column(
        Modifier.padding(24.dp).width(640.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Info("Server", graph.settings.serverName ?: stringResource(R.string.server_product), graph.settings.serverUrl.orEmpty())
        Info("Signed in as", graph.settings.username ?: "Unknown user", null)
        Info("App", "${stringResource(R.string.app_title)} ${BuildConfig.VERSION_NAME}", null)
        SwitchProfileRow()
        BrandRow(onClick = { actions.openConnect() }, icon = Icons.Filled.Refresh, modifier = Modifier.focusRequester(first)) {
            Label("Change server")
        }
        BrandRow(
            onClick = {
                if (!signingOut) {
                    signingOut = true
                    scope.launch {
                        // Revoke the token on the server if it answers quickly; sign out locally regardless.
                        withTimeoutOrNull(3_000) { runCatching { graph.api.logout() } }
                        graph.tokens.clear()
                        actions.toLogin()
                    }
                }
            },
            icon = Icons.Filled.Person,
        ) {
            Label(if (signingOut) "Signing out" else "Sign out")
        }
        BrandRow(onClick = { actions.openAbout() }, icon = Icons.Filled.Info) {
            Label("About")
        }
    }
    LaunchedEffect(Unit) {
        if (focus.toContent) {
            runCatching { first.requestFocus() }
            focus.toContent = false
        }
    }
}

@Composable
private fun Info(label: String, value: String, detail: String?) {
    Column(Modifier.padding(bottom = 12.dp)) {
        Label(label, style = MaterialTheme.typography.labelMedium)
        Text(value, style = MaterialTheme.typography.titleLarge)
        if (!detail.isNullOrEmpty()) Label(detail, style = MaterialTheme.typography.labelSmall)
    }
}
