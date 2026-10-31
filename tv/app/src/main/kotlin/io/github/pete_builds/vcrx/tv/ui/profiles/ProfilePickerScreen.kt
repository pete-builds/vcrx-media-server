package io.github.pete_builds.vcrx.tv.ui.profiles

import android.content.Context
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Refresh
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
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.data.DeviceInfo
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Profile
import io.github.pete_builds.vcrx.tv.net.ProfilesResponse
import io.github.pete_builds.vcrx.tv.net.SwitchRequest
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.ErrorView
import io.github.pete_builds.vcrx.tv.ui.components.FrameCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadingView
import io.github.pete_builds.vcrx.tv.ui.components.TvTextField
import io.github.pete_builds.vcrx.tv.ui.components.Wordmark
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Orange
import io.github.pete_builds.vcrx.tv.ui.theme.White
import kotlinx.coroutines.launch

/**
 * "Who's watching?": the household's profiles, chosen with the remote. Every press
 * goes through [PickerLogic]; this class only loads the list and talks to the server.
 */
class ProfilePickerViewModel(private val graph: AppGraph) : ViewModel() {
    var list by mutableStateOf<LoadState<ProfilesResponse>>(LoadState.Loading)
        private set
    var step by mutableStateOf<PickerStep>(PickerStep.Grid(null))
        private set
    var alwaysAsk by mutableStateOf(graph.profilePrefs.alwaysAsk)
        private set

    /** Signed in already (switching), rather than picking from a trusted network with no session. */
    val signedIn: Boolean get() = graph.tokens.token() != null

    init {
        load()
    }

    fun load() {
        list = LoadState.Loading
        viewModelScope.launch {
            list = try {
                val response = graph.api.profiles()
                step = PickerStep.Grid(PickerLogic.initialFocus(response, graph.profilePrefs.lastProfileId))
                LoadState.Ready(response)
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }

    fun toggleAlwaysAsk() {
        alwaysAsk = !alwaysAsk
        graph.profilePrefs.alwaysAsk = alwaysAsk
    }

    fun choose(profile: Profile, nav: Nav) = handle(PickerLogic.choose(profile), nav)

    fun digit(d: Char, nav: Nav) = (step as? PickerStep.Pin)?.let { handle(PickerLogic.digit(it, d), nav) }

    fun delete(nav: Nav) = (step as? PickerStep.Pin)?.let { handle(PickerLogic.delete(it), nav) }

    fun ok(nav: Nav) = (step as? PickerStep.Pin)?.let { handle(PickerLogic.ok(it), nav) }

    fun password(value: String, nav: Nav) = (step as? PickerStep.Password)?.let { handle(PickerLogic.password(it, value), nav) }

    fun back(nav: Nav) = handle(PickerLogic.back(step), nav)

    private fun handle(result: PickerResult, nav: Nav) {
        step = result.step
        when (val effect = result.effect) {
            is PickerEffect.Switch -> switch(effect, nav)
            PickerEffect.Exit -> nav.exit()
            PickerEffect.None -> Unit
        }
    }

    private fun switch(effect: PickerEffect.Switch, nav: Nav) {
        viewModelScope.launch {
            try {
                val device = DeviceInfo.device(nav.context, graph.settings)
                val response = graph.api.switchProfile(SwitchRequest(effect.profile.id, effect.pin, effect.password, device))
                // The server ended the old session; the new one replaces it here.
                graph.tokens.save(response.token, effect.profile.id)
                graph.settings.username = response.user.displayName ?: response.user.username
                graph.profilePrefs.lastProfileId = effect.profile.id
                nav.done()
            } catch (e: ApiException) {
                val code = when (e) {
                    is ApiException.Unauthorized -> e.code
                    is ApiException.Http -> e.code
                    else -> ""
                }
                step = PickerLogic.failed(step, effect.profile, code, e.message ?: "Could not switch profile.")
            }
        }
    }

    /** Where the screen goes when a switch lands or Back leaves the grid. */
    class Nav(val context: Context, val done: () -> Unit, val exit: () -> Unit)
}

@Composable
fun ProfilePickerScreen() {
    val actions = LocalAppActions.current
    val context = LocalContext.current
    val vm = graphViewModel { ProfilePickerViewModel(it) }
    val nav = remember(actions) { ProfilePickerViewModel.Nav(context, { actions.toBrowse() }, { actions.leaveProfiles() }) }

    BackHandler { vm.back(nav) }

    Box(Modifier.fillMaxSize().padding(48.dp), contentAlignment = Alignment.Center) {
        when (val state = vm.list) {
            LoadState.Loading -> LoadingView()
            is LoadState.Failed -> ErrorView(state.error, onRetry = vm::load)
            is LoadState.Ready -> when (val step = vm.step) {
                is PickerStep.Grid -> ProfileGrid(state.value.profiles, step, vm, nav)
                is PickerStep.Pin -> PinPad(step, vm, nav)
                is PickerStep.Password -> PasswordEntry(step, vm, nav)
            }
        }
    }
}

@Composable
private fun ProfileGrid(profiles: List<Profile>, step: PickerStep.Grid, vm: ProfilePickerViewModel, nav: ProfilePickerViewModel.Nav) {
    val actions = LocalAppActions.current
    val focus = remember { FocusRequester() }
    Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(28.dp)) {
        Wordmark(height = 40.dp)
        Text("Who's watching?", style = MaterialTheme.typography.displaySmall, color = White)
        // Rows of five: the D-pad moves across a row and between rows.
        profiles.chunked(COLUMNS).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(32.dp)) {
                row.forEach { p ->
                    ProfileCard(p, Modifier.then(if (p.id == step.focusId) Modifier.focusRequester(focus) else Modifier)) {
                        vm.choose(p, nav)
                    }
                }
            }
        }
        step.error?.let { Label(it, color = Orange, maxLines = 2) }
        Column(Modifier.width(420.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            BrandRow(onClick = vm::toggleAlwaysAsk, icon = if (vm.alwaysAsk) Icons.Filled.Check else null) {
                Label(if (vm.alwaysAsk) "Asking every time the app opens" else "Ask every time the app opens")
            }
            if (!vm.signedIn) {
                BrandRow(onClick = { actions.openConnect() }, icon = Icons.Filled.Refresh) { Label("Other server") }
            }
        }
    }
    LaunchedEffect(step.focusId) { runCatching { focus.requestFocus() } }
}

@Composable
private fun ProfileCard(profile: Profile, modifier: Modifier, onClick: () -> Unit) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        FrameCard(onClick = onClick, modifier = modifier.size(AVATAR)) {
            Avatar(profile, AVATAR)
        }
        Text(profile.name, style = MaterialTheme.typography.titleMedium, color = White, maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.width(AVATAR))
        val tags = listOfNotNull(if (profile.kid) "Kids" else null, if (profile.needs != Profile.NEEDS_NONE) "PIN" else null)
        Label(tags.joinToString("  "), style = MaterialTheme.typography.labelSmall, color = Gray)
    }
}

/** A profile's picture, or its initials on its colour. */
@Composable
fun Avatar(profile: Profile, size: Dp) {
    val api = LocalContext.current.graph.api
    val background = PickerLogic.parseColor(profile.color)?.let { Color(it) } ?: Graphite
    Box(Modifier.size(size).clip(RoundedCornerShape(size / 6)).background(background), contentAlignment = Alignment.Center) {
        Text(PickerLogic.initials(profile.name), style = MaterialTheme.typography.displaySmall, color = White)
        val url = profile.avatarUrl?.let { api.absoluteUrl(it) }
        if (url != null) {
            AsyncImage(model = url, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        }
    }
}

/**
 * The on-screen PIN pad: a 3 by 4 grid the D-pad walks, plus the remote's number
 * keys. OK sends at four digits or more; the eighth digit sends on its own.
 */
@Composable
private fun PinPad(step: PickerStep.Pin, vm: ProfilePickerViewModel, nav: ProfilePickerViewModel.Nav) {
    val first = remember { FocusRequester() }
    Column(
        Modifier.onPreviewKeyEvent { event ->
            if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
            val digit = DIGIT_KEYS[event.key]
            when {
                digit != null -> vm.digit(digit, nav)
                event.key == Key.Backspace || event.key == Key.Delete -> vm.delete(nav)
                else -> return@onPreviewKeyEvent false
            }
            true
        },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Avatar(step.profile, 96.dp)
        Text(step.profile.name, style = MaterialTheme.typography.headlineSmall, color = White)
        Label(if (step.busy) "Checking" else "Enter the PIN", color = Gray)
        PinDots(step.digits.length)
        Label(step.error ?: " ", color = Orange, maxLines = 2)
        PAD_ROWS.forEachIndexed { r, keys ->
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                keys.forEachIndexed { c, key ->
                    val modifier = Modifier.width(96.dp).then(if (r == 0 && c == 0) Modifier.focusRequester(first) else Modifier)
                    when (key) {
                        DELETE -> BrandButton("Del", onClick = { vm.delete(nav) }, modifier = modifier)
                        OK -> BrandButton("OK", onClick = { vm.ok(nav) }, modifier = modifier, primary = true)
                        else -> BrandButton(key, onClick = { vm.digit(key[0], nav) }, modifier = modifier)
                    }
                }
            }
        }
    }
    LaunchedEffect(step.profile.id) { runCatching { first.requestFocus() } }
}

@Composable
private fun PinDots(count: Int) {
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
        repeat(maxOf(PickerLogic.MIN_PIN, count)) { i ->
            val filled = i < count
            Box(
                Modifier.size(18.dp).clip(CircleShape)
                    .background(if (filled) Orange else Color.Transparent)
                    .border(2.dp, if (filled) Orange else Gray, CircleShape),
            )
        }
    }
}

@Composable
private fun PasswordEntry(step: PickerStep.Password, vm: ProfilePickerViewModel, nav: ProfilePickerViewModel.Nav) {
    var value by remember(step.profile.id) { mutableStateOf("") }
    val field = remember { FocusRequester() }
    val submit = {
        vm.password(value, nav)
        value = ""
    }
    Column(Modifier.width(560.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Avatar(step.profile, 96.dp)
        Text(step.profile.name, style = MaterialTheme.typography.headlineSmall, color = White)
        Label("An admin's profile needs its password here", color = Gray)
        TvTextField(
            value = value,
            onValueChange = { value = it },
            label = "Password",
            password = true,
            imeAction = ImeAction.Done,
            onImeAction = submit,
            modifier = Modifier.focusRequester(field),
        )
        step.error?.let { Label(it, color = Orange, maxLines = 2) }
        Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            BrandButton(if (step.busy) "Checking" else "Continue", onClick = submit, primary = true)
            BrandButton("Back", onClick = { vm.back(nav) })
        }
    }
    LaunchedEffect(step.profile.id) { runCatching { field.requestFocus() } }
}

private const val COLUMNS = 5
private val AVATAR = 148.dp
private const val DELETE = "del"
private const val OK = "ok"
private val PAD_ROWS = listOf(listOf("1", "2", "3"), listOf("4", "5", "6"), listOf("7", "8", "9"), listOf(DELETE, "0", OK))
private val DIGIT_KEYS = mapOf(
    Key.Zero to '0', Key.One to '1', Key.Two to '2', Key.Three to '3', Key.Four to '4',
    Key.Five to '5', Key.Six to '6', Key.Seven to '7', Key.Eight to '8', Key.Nine to '9',
    Key.NumPad0 to '0', Key.NumPad1 to '1', Key.NumPad2 to '2', Key.NumPad3 to '3', Key.NumPad4 to '4',
    Key.NumPad5 to '5', Key.NumPad6 to '6', Key.NumPad7 to '7', Key.NumPad8 to '8', Key.NumPad9 to '9',
)
