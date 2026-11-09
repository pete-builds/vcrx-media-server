package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.mobile.BuildConfig
import io.github.pete_builds.vcrx.mobile.graph
import kotlinx.coroutines.launch

/** Who this is, which server, and signing out. */
@Composable
fun SettingsScreen(onSwitchProfile: () -> Unit, onSignedOut: () -> Unit) {
    val graph = LocalContext.current.graph
    val me by graph.me.collectAsState()
    val scope = rememberCoroutineScope()
    var confirm by remember { mutableStateOf(false) }
    val invited = graph.tokens.isInvitedDevice
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Server", style = SectionTitle)
        Text(graph.settings.serverName?.takeIf { it.isNotBlank() } ?: "VCRX")
        Text(graph.settings.serverUrl.orEmpty(), color = Brand.Muted)
        Text("Signed in", style = SectionTitle)
        Text(me?.displayName ?: me?.username ?: "Unknown")
        if (invited) Text("An invited device: it streams but cannot download.", color = Brand.Muted)
        if (!invited) BrandButton("Switch profile", onClick = onSwitchProfile)
        QuietButton("Sign out", onClick = { confirm = true })
        Text("About", style = SectionTitle)
        Text("VCRX for Android ${BuildConfig.VERSION_NAME}", color = Brand.Muted)
        Text("Free software under the AGPL. No telemetry; it talks only to your server.", color = Brand.Muted, style = MaterialTheme.typography.bodySmall)
    }
    if (confirm) {
        AlertDialog(
            onDismissRequest = { confirm = false },
            title = { Text("Sign out?") },
            text = {
                Text(
                    if (invited) "This device gives up its invite. You will need a new link to join again." else "Downloads stay on this device.",
                )
            },
            confirmButton = {
                BrandButton("Sign out", onClick = {
                    confirm = false
                    scope.launch {
                        runCatching { graph.api.logout() }
                        graph.tokens.clear()
                        graph.forgetSession()
                        graph.prefs.clearAccountData()
                        graph.offlineAudio.clearAccountData()
                        onSignedOut()
                    }
                })
            },
            dismissButton = { QuietButton("Cancel", onClick = { confirm = false }) },
        )
    }
}
