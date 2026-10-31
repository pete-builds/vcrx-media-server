package io.github.pete_builds.vcrx.tv.ui.connect

import androidx.activity.compose.BackHandler
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
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
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
import io.github.pete_builds.vcrx.tv.net.AutoLoginRequest
import io.github.pete_builds.vcrx.tv.net.LoginRequest
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.TvTextField
import io.github.pete_builds.vcrx.tv.ui.components.Wordmark
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Orange
import kotlinx.coroutines.launch

class LoginViewModel(private val graph: AppGraph) : ViewModel() {
    var username by mutableStateOf(graph.settings.username.orEmpty())
    var password by mutableStateOf("")
    var busy by mutableStateOf(false)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var unreachable by mutableStateOf(false)
        private set

    val serverLabel: String = listOfNotNull(graph.settings.serverName, graph.settings.serverUrl).joinToString("  ")

    /** True while checking whether this network may sign in without a password. */
    var checkingAuto by mutableStateOf(true)
        private set
    private var autoTried = false

    /**
     * On the server's trusted networks (the home LAN, Tailscale) there is no
     * password to type: the server says so in GET /api/v1/server, and
     * POST /api/v1/auth/auto signs this device in. Anything else shows the form.
     */
    fun tryAutoSignIn(context: android.content.Context, onPicker: () -> Unit, onDone: () -> Unit) {
        if (autoTried) return
        autoTried = true
        val base = graph.settings.serverUrl
        // Switch user asked for the form: someone else is signing in on this TV.
        val skip = graph.settings.skipAutoSignIn
        graph.settings.skipAutoSignIn = false
        if (base == null || skip) {
            checkingAuto = false
            return
        }
        viewModelScope.launch {
            try {
                val info = graph.api.serverInfo(base)
                if (info.profilePicker) {
                    // This network gets "Who's watching?" rather than one fixed user.
                    onPicker()
                    return@launch
                }
                if (info.autoSignIn) {
                    val response = graph.api.autoLogin(AutoLoginRequest(DeviceInfo.device(context, graph.settings)))
                    graph.tokens.save(response.token)
                    graph.settings.username = response.user.displayName ?: response.user.username
                    onDone()
                    return@launch
                }
            } catch (e: ApiException) {
                // Not trusted here, or the server is down: the form says what is wrong.
                android.util.Log.i("Login", "Sign-in without a password not available: ${e.message}")
            }
            checkingAuto = false
        }
    }

    fun signIn(context: android.content.Context, onDone: () -> Unit) {
        if (busy) return
        if (username.isBlank() || password.isEmpty()) {
            error = "Enter your username and password."
            return
        }
        busy = true
        error = null
        unreachable = false
        viewModelScope.launch {
            try {
                val device = DeviceInfo.device(context, graph.settings)
                val response = graph.api.login(LoginRequest(username.trim(), password, device))
                graph.tokens.save(response.token)
                graph.settings.username = response.user.displayName ?: response.user.username
                password = ""
                onDone()
            } catch (e: ApiException.Unreachable) {
                unreachable = true
                error = e.message
            } catch (e: ApiException) {
                error = e.message
            } finally {
                busy = false
            }
        }
    }
}

@Composable
fun LoginScreen() {
    val actions = LocalAppActions.current
    val context = LocalContext.current
    val vm = graphViewModel { LoginViewModel(it) }
    val userFocus = remember { FocusRequester() }
    val passwordFocus = remember { FocusRequester() }
    val signInFocus = remember { FocusRequester() }
    val submit = { vm.signIn(context) { actions.toBrowse() } }

    // Back from Login means "wrong server": go to Connect rather than leave the app.
    BackHandler { actions.toConnect() }

    LaunchedEffect(Unit) { vm.tryAutoSignIn(context, onPicker = { actions.toProfiles() }) { actions.toBrowse() } }
    if (vm.checkingAuto) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(20.dp)) {
                Wordmark(height = 56.dp)
                Label("Signing in", style = MaterialTheme.typography.labelLarge, color = Gray)
            }
        }
        return
    }

    Box(Modifier.fillMaxSize().padding(48.dp), contentAlignment = Alignment.Center) {
        Column(Modifier.width(560.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Wordmark(height = 56.dp)
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("Sign in".uppercase(), style = MaterialTheme.typography.headlineSmall, color = Orange)
                Label(vm.serverLabel, style = MaterialTheme.typography.labelSmall, color = Gray)
            }
            TvTextField(
                value = vm.username,
                onValueChange = { vm.username = it },
                label = "Username",
                imeAction = ImeAction.Next,
                onImeAction = { runCatching { passwordFocus.requestFocus() } },
                modifier = Modifier.focusRequester(userFocus),
            )
            TvTextField(
                value = vm.password,
                onValueChange = { vm.password = it },
                label = "Password",
                password = true,
                imeAction = ImeAction.Done,
                onImeAction = submit,
                // Down from the full-width field would land on the nearer centre,
                // Other server; after a password the next step is Sign in.
                modifier = Modifier.focusRequester(passwordFocus).focusProperties { down = signInFocus },
            )
            vm.error?.let { ErrorLine(it) }
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                BrandButton(
                    if (vm.busy) "Signing in" else "Sign in",
                    onClick = submit,
                    primary = true,
                    modifier = Modifier.focusRequester(signInFocus),
                )
                BrandButton(if (vm.unreachable) "Change server" else "Other server", onClick = { actions.toConnect() })
            }
        }
    }
    LaunchedEffect(Unit) {
        runCatching { if (vm.username.isBlank()) userFocus.requestFocus() else passwordFocus.requestFocus() }
    }
}
