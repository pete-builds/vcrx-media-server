package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.mobile.R
import io.github.pete_builds.vcrx.mobile.data.DeviceInfo
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.AutoLoginRequest
import io.github.pete_builds.vcrx.mobile.net.LoginRequest
import io.github.pete_builds.vcrx.mobile.net.Profile
import io.github.pete_builds.vcrx.mobile.net.RedeemRequest
import io.github.pete_builds.vcrx.mobile.net.ServerAddress
import io.github.pete_builds.vcrx.mobile.net.SwitchRequest
import io.github.pete_builds.vcrx.mobile.offline.InviteLink
import kotlinx.coroutines.launch

/** A narrow, centred column for the sign-in screens, on a phone or a tablet. */
@Composable
private fun AuthFrame(title: String, content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize().safeDrawingPadding().imePadding(), contentAlignment = Alignment.TopCenter) {
        Column(
            Modifier.widthIn(max = 480.dp).fillMaxWidth().verticalScroll(rememberScrollState()).padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            androidx.compose.foundation.Image(
                painterResource(R.drawable.brand_wordmark),
                contentDescription = "VCRX",
                modifier = Modifier.padding(top = 32.dp, bottom = 8.dp).height(48.dp),
            )
            Text(title, style = MaterialTheme.typography.headlineSmall)
            content()
        }
    }
}

@Composable
private fun ErrorLine(text: String?) {
    if (text != null) Text(text, color = Brand.Error, style = MaterialTheme.typography.bodyMedium)
}

/** The server address, checked against GET /api/v1/server before it is kept. */
@Composable
fun ConnectScreen(onConnected: (profilePicker: Boolean) -> Unit, onInvite: () -> Unit) {
    val graph = LocalContext.current.graph
    val scope = rememberCoroutineScope()
    var address by remember { mutableStateOf(graph.settings.serverUrl.orEmpty()) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    var cleartext by remember { mutableStateOf<String?>(null) }

    fun connect(base: String) {
        busy = true
        error = null
        scope.launch {
            try {
                val info = graph.api.serverInfo(base)
                graph.settings.serverUrl = base
                graph.settings.serverName = info.name
                onConnected(info.profilePicker)
            } catch (e: ApiException) {
                error = e.message
            } finally {
                busy = false
            }
        }
    }

    AuthFrame("Connect to your server") {
        Text("The address of your VCRX server, such as 192.168.1.20 on your home network or an https address.", color = Brand.Muted)
        OutlinedTextField(
            value = address,
            onValueChange = { address = it },
            label = { Text("Server address") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            modifier = Modifier.fillMaxWidth(),
        )
        ErrorLine(error)
        BrandButton("Connect", enabled = !busy, modifier = Modifier.fillMaxWidth(), onClick = {
            val base = ServerAddress.normalize(address)
            when {
                base == null -> error = "That is not a server address."
                ServerAddress.cleartextToPublicHost(base) -> cleartext = base
                else -> connect(base)
            }
        })
        QuietButton("Open an invite link", onClick = onInvite, modifier = Modifier.fillMaxWidth())
    }

    cleartext?.let { base ->
        AlertDialog(
            onDismissRequest = { cleartext = null },
            title = { Text("Unencrypted connection") },
            text = {
                Text(
                    "$base is not on a private network and does not use https. Your password, your session and " +
                        "everything you play would cross the internet unencrypted. Use an https address if you can.",
                )
            },
            confirmButton = { QuietButton("Connect anyway", onClick = { cleartext = null; connect(base) }) },
            dismissButton = { BrandButton("Go back", onClick = { cleartext = null }) },
        )
    }
}

/** Username and password, or no password at all on a network the server trusts. */
@Composable
fun LoginScreen(onSignedIn: () -> Unit, onProfiles: () -> Unit, onChangeServer: () -> Unit) {
    val context = LocalContext.current
    val graph = context.graph
    val scope = rememberCoroutineScope()
    var username by remember { mutableStateOf(graph.settings.username.orEmpty()) }
    var password by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    var auto by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) {
        val info = runCatching { graph.api.serverInfo(graph.settings.serverUrl ?: return@LaunchedEffect) }.getOrNull()
        auto = info?.autoSignIn == true
    }

    AuthFrame("Sign in") {
        Text(graph.settings.serverName?.takeIf { it.isNotBlank() } ?: graph.settings.serverUrl.orEmpty(), color = Brand.Muted)
        OutlinedTextField(username, { username = it }, label = { Text("Username") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(
            password,
            { password = it },
            label = { Text("Password") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth(),
        )
        ErrorLine(error)
        BrandButton("Sign in", enabled = !busy && username.isNotBlank() && password.isNotEmpty(), modifier = Modifier.fillMaxWidth(), onClick = {
            busy = true
            error = null
            scope.launch {
                try {
                    val r = graph.api.login(LoginRequest(username.trim(), password, DeviceInfo.device(context, graph.settings)))
                    graph.tokens.save(r.token)
                    graph.settings.username = username.trim()
                    graph.setSignedIn(r.user)
                    onSignedIn()
                } catch (e: ApiException) {
                    error = e.message
                } finally {
                    busy = false
                }
            }
        })
        if (auto) {
            BrandButton("Sign in without a password", enabled = !busy, modifier = Modifier.fillMaxWidth(), onClick = {
                busy = true
                scope.launch {
                    try {
                        val r = graph.api.autoLogin(AutoLoginRequest(DeviceInfo.device(context, graph.settings)))
                        graph.tokens.save(r.token)
                        graph.setSignedIn(r.user)
                        onSignedIn()
                    } catch (e: ApiException) {
                        error = e.message
                    } finally {
                        busy = false
                    }
                }
            })
        }
        QuietButton("Who's watching? Choose a profile", onClick = onProfiles, modifier = Modifier.fillMaxWidth())
        QuietButton("Change server", onClick = onChangeServer, modifier = Modifier.fillMaxWidth())
    }
}

/** "Who's watching?": the household's profiles, with a PIN or password where a profile has one. */
@Composable
fun ProfilesScreen(onSignedIn: () -> Unit, onPassword: () -> Unit) {
    val context = LocalContext.current
    val graph = context.graph
    val scope = rememberCoroutineScope()
    var asking by remember { mutableStateOf<Profile?>(null) }
    var secret by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }

    fun switch(p: Profile, pin: String?, password: String?) {
        scope.launch {
            try {
                val r = graph.api.switchProfile(SwitchRequest(p.id, pin, password, DeviceInfo.device(context, graph.settings)))
                graph.tokens.save(r.token, p.id)
                graph.setSignedIn(r.user)
                asking = null
                onSignedIn()
            } catch (e: ApiException) {
                error = e.message
            }
        }
    }

    Box(Modifier.fillMaxSize().safeDrawingPadding()) {
        Loadable(Unit, { graph.api.profiles() }) { answer ->
            LazyColumn(Modifier.fillMaxSize().padding(horizontal = 24.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                item { Text("Who's watching?", style = MaterialTheme.typography.headlineMedium, modifier = Modifier.padding(vertical = 24.dp)) }
                items(answer.profiles, key = { it.id }) { p ->
                    Row(
                        Modifier.fillMaxWidth().clip(MaterialTheme.shapes.medium).clickable {
                            error = null
                            secret = ""
                            if (p.needs == Profile.NEEDS_NONE) switch(p, null, null) else asking = p
                        }.padding(12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(16.dp),
                    ) {
                        val bg = runCatching { Color(android.graphics.Color.parseColor(p.color)) }.getOrDefault(Brand.Tint)
                        Box(Modifier.size(48.dp).clip(CircleShape).background(bg), contentAlignment = Alignment.Center) {
                            Text(p.name.take(1).uppercase(), style = MaterialTheme.typography.titleLarge)
                        }
                        Column {
                            Text(p.name, style = MaterialTheme.typography.titleMedium)
                            val note = when (p.needs) {
                                Profile.NEEDS_PIN -> "PIN"
                                Profile.NEEDS_PASSWORD -> "Password"
                                else -> if (p.kid) "Kids" else null
                            }
                            note?.let { Text(it, color = Brand.Muted, style = MaterialTheme.typography.bodySmall) }
                        }
                    }
                }
                item { error?.let { ErrorLine(it) } }
                item { QuietButton("Sign in with a password instead", onClick = onPassword) }
            }
        }
    }

    asking?.let { p ->
        val pin = p.needs == Profile.NEEDS_PIN
        AlertDialog(
            onDismissRequest = { asking = null },
            title = { Text(p.name) },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        secret,
                        { secret = it },
                        label = { Text(if (pin) "PIN" else "Password") },
                        singleLine = true,
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = if (pin) KeyboardType.NumberPassword else KeyboardType.Password),
                    )
                    ErrorLine(error)
                }
            },
            confirmButton = { BrandButton("Continue", enabled = secret.isNotEmpty(), onClick = { switch(p, secret.takeIf { pin }, secret.takeIf { !pin }) }) },
            dismissButton = { QuietButton("Cancel", onClick = { asking = null }) },
        )
    }
}

/** Joins as an invited device: paste the link from Settings > Devices. Such a device streams and never downloads. */
@Composable
fun InviteScreen(onSignedIn: () -> Unit, onBack: () -> Unit) {
    val context = LocalContext.current
    val graph = context.graph
    val scope = rememberCoroutineScope()
    var text by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    AuthFrame("Open an invite link") {
        Text("Paste the invite link someone sent you. It works once, on one device, and this device can then stream but not download.", color = Brand.Muted)
        OutlinedTextField(text, { text = it }, label = { Text("Invite link") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        ErrorLine(error)
        BrandButton("Join", enabled = !busy && text.isNotBlank(), modifier = Modifier.fillMaxWidth(), onClick = {
            val link = InviteLink.parse(text)
            val base = link?.serverUrl ?: graph.settings.serverUrl
            when {
                link == null -> error = "That is not an invite link."
                base == null -> error = "Paste the whole link, or connect to the server first."
                else -> {
                    busy = true
                    error = null
                    scope.launch {
                        try {
                            val info = graph.api.serverInfo(base)
                            graph.settings.serverUrl = base
                            graph.settings.serverName = info.name
                            val r = graph.api.redeemInvite(RedeemRequest(link.secret, DeviceInfo.device(context, graph.settings)))
                            graph.tokens.saveInvited(r.token)
                            graph.setSignedIn(r.user)
                            onSignedIn()
                        } catch (e: ApiException) {
                            error = e.message
                        } finally {
                            busy = false
                        }
                    }
                }
            }
        })
        QuietButton("Back", onClick = onBack, modifier = Modifier.fillMaxWidth())
    }
}
