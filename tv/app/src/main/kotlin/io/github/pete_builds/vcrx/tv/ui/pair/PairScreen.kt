package io.github.pete_builds.vcrx.tv.ui.pair

import android.content.Context
import android.graphics.BitmapFactory
import android.util.Base64
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.data.DeviceInfo
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.PairPollRequest
import io.github.pete_builds.vcrx.tv.net.PairStartRequest
import io.github.pete_builds.vcrx.tv.net.RedeemRequest
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.TvTextField
import io.github.pete_builds.vcrx.tv.ui.components.Wordmark
import io.github.pete_builds.vcrx.tv.ui.connect.ErrorLine
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.White
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Pair with a phone: the TV shows a code and a QR code, an admin's phone on the
 * same network approves it with a scope, and the TV signs in as its own invited
 * device. Or type an invite. Every step goes through [PairingLogic]; this class
 * only talks to the server and keeps time.
 */
class PairViewModel(private val graph: AppGraph) : ViewModel() {
    var state by mutableStateOf<PairingState>(PairingState.Starting)
        private set
    var now by mutableStateOf(System.currentTimeMillis())
        private set
    var invite by mutableStateOf("")
    var inviteError by mutableStateOf<String?>(null)
        private set
    var joining by mutableStateOf(false)
        private set
    private var job: Job? = null

    val serverLabel: String = listOfNotNull(graph.settings.serverName, graph.settings.serverUrl).joinToString("  ")

    /** Starts pairing unless it already runs, as when the screen is drawn again. */
    fun ensureStarted(context: Context, onSignedIn: () -> Unit) {
        if (job == null) start(context, onSignedIn)
    }

    /** Asks for a fresh code and polls until it is approved, turned away or runs out. */
    fun start(context: Context, onSignedIn: () -> Unit) {
        job?.cancel()
        state = PairingState.Starting
        job = viewModelScope.launch {
            val device = DeviceInfo.device(context, graph.settings)
            var s: PairingState = try {
                PairingLogic.started(graph.api.pairStart(PairStartRequest(device)), System.currentTimeMillis())
            } catch (e: ApiException) {
                PairingState.Failed(e.message ?: "Could not start pairing.")
            }
            state = s
            while (s is PairingState.Showing) {
                val due = System.currentTimeMillis() + s.intervalMs
                while (System.currentTimeMillis() < due) {
                    now = System.currentTimeMillis()
                    delay(minOf(1_000L, due - now).coerceAtLeast(1L))
                }
                now = System.currentTimeMillis()
                s = PairingLogic.afterPoll(s, poll(s.deviceCode), System.currentTimeMillis())
                state = s
            }
            if (s is PairingState.Approved) {
                signIn(s.token, s.name)
                onSignedIn()
            }
        }
    }

    private suspend fun poll(deviceCode: String): PollOutcome = try {
        PollOutcome.Answer(graph.api.pairPoll(PairPollRequest(deviceCode)))
    } catch (e: ApiException.Unreachable) {
        PollOutcome.Unreachable(e.message ?: "The server did not answer.")
    } catch (e: ApiException.Http) {
        when (e.status) {
            404 -> PollOutcome.Gone
            429 -> PollOutcome.SlowDown
            else -> PollOutcome.Error(e.message ?: "The server refused pairing.")
        }
    } catch (e: ApiException) {
        PollOutcome.Error(e.message ?: "The server refused pairing.")
    }

    /** An invite typed with the remote, or its whole link. */
    fun join(context: Context, onSignedIn: () -> Unit) {
        if (joining) return
        val secret = PairingLogic.normalizeInvite(invite)
        if (secret == null) {
            inviteError = "An invite is 32 letters and digits, from the end of its link."
            return
        }
        joining = true
        inviteError = null
        viewModelScope.launch {
            try {
                val response = graph.api.redeemInvite(RedeemRequest(secret, DeviceInfo.device(context, graph.settings)))
                signIn(response.token, response.user.displayName ?: response.user.username)
                job?.cancel()
                onSignedIn()
            } catch (e: ApiException) {
                inviteError = e.message
            } finally {
                joining = false
            }
        }
    }

    private fun signIn(token: String, name: String) {
        graph.tokens.saveInvited(token)
        graph.settings.username = name
    }
}

/** The PNG the server drew, or null when it is missing or not an image. */
private fun decodeQr(base64: String): ImageBitmap? = runCatching {
    val bytes = Base64.decode(base64, Base64.DEFAULT)
    BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap()
}.getOrNull()

@Composable
fun PairScreen() {
    val actions = LocalAppActions.current
    val context = LocalContext.current
    val vm = graphViewModel { PairViewModel(it) }
    val firstFocus = remember { FocusRequester() }
    val done = { actions.toBrowse() }

    LaunchedEffect(Unit) { vm.ensureStarted(context, done) }

    Box(Modifier.fillMaxSize().padding(48.dp), contentAlignment = Alignment.Center) {
        Row(horizontalArrangement = Arrangement.spacedBy(48.dp), verticalAlignment = Alignment.CenterVertically) {
            val s = vm.state
            val qrPng = (s as? PairingState.Showing)?.qrPng
            val qr = remember(qrPng) { qrPng?.let { decodeQr(it) } }
            Box(
                Modifier.size(280.dp).clip(RoundedCornerShape(12.dp)).background(if (qr != null) Color.White else Color.Transparent),
                contentAlignment = Alignment.Center,
            ) {
                if (qr != null) {
                    // Nearest-neighbour scaling keeps every module of the code sharp.
                    Image(qr, contentDescription = "QR code to pair this TV", modifier = Modifier.size(264.dp), filterQuality = FilterQuality.None)
                }
            }
            Column(Modifier.width(560.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Wordmark(height = 48.dp)
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text("Pair with a phone", style = MaterialTheme.typography.headlineSmall, color = White)
                    Label(vm.serverLabel, style = MaterialTheme.typography.labelSmall, color = Gray)
                }
                when (s) {
                    PairingState.Starting -> Label("Getting a code", color = Gray)
                    is PairingState.Showing -> {
                        Text(s.userCode, style = MaterialTheme.typography.displayLarge)
                        Text(
                            "On a phone signed in to this server as an admin, scan the code, or open Settings, " +
                                "Devices and type it. You choose what this TV may watch.",
                            style = MaterialTheme.typography.bodyLarge,
                        )
                        val left = PairingLogic.secondsLeft(s, vm.now)
                        Label("Code runs out in ${left / 60}:${(left % 60).toString().padStart(2, '0')}", color = Gray)
                        s.note?.let { Label("Waiting for the server: $it", color = Gray, maxLines = 2) }
                    }
                    is PairingState.Approved -> Label("Approved. Signing in as ${s.name}", color = Gray)
                    is PairingState.Expired -> ErrorLine(s.message)
                    is PairingState.Failed -> ErrorLine(s.message)
                }
                Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    if (s is PairingState.Expired || s is PairingState.Failed) {
                        BrandButton("New code", onClick = { vm.start(context, done) }, primary = true, modifier = Modifier.focusRequester(firstFocus))
                    }
                }
                Column(Modifier.padding(top = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Label("Or type an invite you were sent", style = MaterialTheme.typography.labelMedium)
                    TvTextField(
                        value = vm.invite,
                        onValueChange = { vm.invite = it },
                        label = "Invite",
                        placeholder = "32 letters and digits",
                        imeAction = ImeAction.Go,
                        onImeAction = { vm.join(context, done) },
                        modifier = if (s is PairingState.Expired || s is PairingState.Failed) Modifier else Modifier.focusRequester(firstFocus),
                    )
                    vm.inviteError?.let { ErrorLine(it) }
                    BrandButton(if (vm.joining) "Joining" else "Join with invite", onClick = { vm.join(context, done) })
                }
            }
        }
    }
    LaunchedEffect(vm.state is PairingState.Expired || vm.state is PairingState.Failed) { runCatching { firstFocus.requestFocus() } }
}
