package io.github.pete_builds.vcrx.tv.ui.connect

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.R
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.ServerAddress
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandDialog
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.TvTextField
import io.github.pete_builds.vcrx.tv.ui.components.Wordmark
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import kotlinx.coroutines.launch

/** The contract's fixed product id in GET /api/v1/server, not a display name. */
const val PRODUCT = "vcrx-media-server"
const val API_VERSION = 1

class ConnectViewModel(private val graph: AppGraph) : ViewModel() {
    var address by mutableStateOf(graph.settings.serverUrl.orEmpty())
    var busy by mutableStateOf(false)
        private set
    var error by mutableStateOf<String?>(null)
        private set

    /**
     * A plain http:// address to a host that is not on a private network, waiting
     * for the viewer to confirm before anything is sent to it.
     */
    var cleartextWarning by mutableStateOf<String?>(null)
        private set
    private var afterWarning: ((needsLogin: Boolean) -> Unit)? = null

    /**
     * Checks the address is a VCRX server before saving it. [onDone] says whether
     * sign in is needed; [onEmpty] runs when nothing was typed, to open the keyboard.
     * An unencrypted address to a public host waits for [confirmCleartext] first.
     */
    fun connect(onEmpty: () -> Unit = {}, onDone: (needsLogin: Boolean) -> Unit) {
        if (busy) return
        if (address.isBlank()) {
            error = "Type your server's address first, for example 192.168.1.20:3750, then press Connect."
            onEmpty()
            return
        }
        val base = ServerAddress.normalize(address)
        if (base == null) {
            error = "That is not an address. Type the server's LAN address, for example 192.168.1.20:3750."
            return
        }
        error = null
        if (ServerAddress.cleartextToPublicHost(base)) {
            cleartextWarning = base
            afterWarning = onDone
            return
        }
        probe(base, onDone)
    }

    /** The viewer chose to connect over plain http:// to a public host anyway. */
    fun confirmCleartext() {
        val base = cleartextWarning ?: return
        val onDone = afterWarning ?: return
        cancelCleartext()
        probe(base, onDone)
    }

    fun cancelCleartext() {
        cleartextWarning = null
        afterWarning = null
    }

    private fun probe(base: String, onDone: (needsLogin: Boolean) -> Unit) {
        if (busy) return
        busy = true
        error = null
        viewModelScope.launch {
            try {
                val info = graph.api.serverInfo(base)
                when {
                    info.product != PRODUCT -> error = graph.resources.getString(R.string.error_not_server, base)
                    info.apiVersion != API_VERSION ->
                        error = "This server speaks API version ${info.apiVersion}; this app needs version $API_VERSION."
                    else -> {
                        val sameServer = graph.settings.serverUrl == base
                        graph.settings.serverUrl = base
                        graph.settings.serverName = info.name.ifBlank { null }
                        val signedIn = sameServer && graph.tokens.token() != null
                        if (!signedIn) graph.tokens.clear()
                        address = base
                        onDone(!signedIn)
                    }
                }
            } catch (e: ApiException.Unreachable) {
                error = "Could not reach $base. Check the address and that the server is running. ${e.cause?.message.orEmpty()}"
            } catch (e: ApiException) {
                error = graph.resources.getString(R.string.error_unlike_server, base, e.message.orEmpty())
            } finally {
                busy = false
            }
        }
    }
}

@Composable
fun ConnectScreen() {
    val actions = LocalAppActions.current
    val vm = graphViewModel { ConnectViewModel(it) }
    val fieldFocus = remember { FocusRequester() }
    val keyboard = LocalSoftwareKeyboardController.current
    // Nothing typed: back to the field with the keyboard up, not just an error.
    val typeFirst = {
        runCatching { fieldFocus.requestFocus() }
        keyboard?.show()
        Unit
    }
    val submit = { vm.connect(onEmpty = typeFirst) { needsLogin -> if (needsLogin) actions.toLogin() else actions.toBrowse() } }

    Box(Modifier.fillMaxSize().padding(48.dp), contentAlignment = Alignment.Center) {
        Column(Modifier.width(560.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Wordmark(height = 56.dp)
            Label(stringResource(R.string.tagline), style = MaterialTheme.typography.labelLarge, color = Gray)
            Text(
                "Connect to your server".uppercase(),
                style = MaterialTheme.typography.headlineSmall,
                color = Accent,
                modifier = Modifier.padding(top = 12.dp),
            )
            TvTextField(
                value = vm.address,
                onValueChange = { vm.address = it },
                label = "Server address",
                // Not an address: an example in the box read as one already typed.
                placeholder = "Type your server's address",
                keyboardType = KeyboardType.Uri,
                imeAction = ImeAction.Go,
                onImeAction = submit,
                modifier = Modifier.focusRequester(fieldFocus),
            )
            Text(
                "For example 192.168.1.20:3750: a LAN IP or hostname, with the port if it is not 3750. Plain HTTP on your own network; " +
                    "HTTPS arrives later through a reverse proxy.",
                style = MaterialTheme.typography.bodyMedium,
            )
            vm.error?.let { ErrorLine(it) }
            vm.cleartextWarning?.let { CleartextWarning(it, onBack = vm::cancelCleartext, onConnect = vm::confirmCleartext) }
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                BrandButton(if (vm.busy) "Connecting" else "Connect", onClick = submit, primary = true)
                // Connect, then pair as an invited device rather than sign in. Back
                // from pairing lands on the sign-in form.
                BrandButton(
                    "Pair with a phone",
                    onClick = {
                        vm.connect(onEmpty = typeFirst) { needsLogin ->
                            if (needsLogin) {
                                actions.toLogin()
                                actions.openPair()
                            } else {
                                actions.toBrowse()
                            }
                        }
                    },
                )
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { fieldFocus.requestFocus() } }
}

/** Asks before sending a password and streams unencrypted across the internet. Go back has focus. */
@Composable
private fun CleartextWarning(address: String, onBack: () -> Unit, onConnect: () -> Unit) {
    val back = remember { FocusRequester() }
    BrandDialog("Unencrypted connection", onDismiss = onBack) {
        Text(
            "$address is not on your home network, and http:// does not encrypt anything. Your password and everything " +
                "you watch would cross the internet where others can read it. Use an https:// address if the server has one.",
            style = MaterialTheme.typography.bodyLarge,
            modifier = Modifier.padding(start = 9.dp, end = 9.dp, bottom = 16.dp),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            BrandButton("Go back", onClick = onBack, primary = true, modifier = Modifier.focusRequester(back))
            BrandButton("Connect anyway", onClick = onConnect)
        }
    }
    LaunchedEffect(address) { runCatching { back.requestFocus() } }
}

@Composable
fun ErrorLine(message: String) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Label("Error", style = MaterialTheme.typography.labelMedium)
        Text(message, style = MaterialTheme.typography.bodyLarge)
    }
}
