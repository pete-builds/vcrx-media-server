package io.github.pete_builds.vcrx.tv.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.theme.Orange

/** What every screen that loads data can be showing. */
sealed interface LoadState<out T> {
    data object Loading : LoadState<Nothing>
    data class Ready<T>(val value: T) : LoadState<T>
    data class Failed(val error: Throwable) : LoadState<Nothing>
}

/** Renders loading and failure for [state] and hands the loaded value to [content]. */
@Composable
fun <T> LoadStateContent(
    state: LoadState<T>,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable (T) -> Unit,
) {
    when (state) {
        LoadState.Loading -> LoadingView(modifier)
        is LoadState.Failed -> ErrorView(state.error, onRetry, modifier)
        is LoadState.Ready -> content(state.value)
    }
}

/** A screen still loading: the loading bar top left, nothing else (no spinner, no word). */
@Composable
fun LoadingView(modifier: Modifier = Modifier, message: String = "Loading") {
    Box(modifier.fillMaxSize()) {
        LoadingBar(Modifier.align(Alignment.TopStart), description = message)
    }
}

@Composable
fun EmptyView(title: String, detail: String? = null, modifier: Modifier = Modifier) {
    MessageView(title = title, detail = detail, modifier = modifier)
}

/**
 * A failure with a way out. Unreachable servers get Retry and Change server, since
 * the address may simply be wrong or the server down; anything else gets Retry.
 */
@Composable
fun ErrorView(error: Throwable, onRetry: () -> Unit, modifier: Modifier = Modifier) {
    val actions = LocalAppActions.current
    if (error is ApiException.Unreachable) {
        MessageView(
            title = "Server unreachable",
            detail = error.message,
            modifier = modifier,
            primary = "Retry" to onRetry,
            secondary = "Change server" to { actions.openConnect() },
        )
    } else {
        MessageView(
            title = "Something went wrong",
            detail = error.message ?: error.javaClass.simpleName,
            modifier = modifier,
            primary = "Retry" to onRetry,
        )
    }
}

@Composable
fun MessageView(
    title: String,
    detail: String?,
    modifier: Modifier = Modifier,
    primary: Pair<String, () -> Unit>? = null,
    secondary: Pair<String, () -> Unit>? = null,
) {
    val focus = remember { FocusRequester() }
    Box(modifier.fillMaxSize().padding(48.dp), contentAlignment = Alignment.Center) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
            modifier = Modifier.widthIn(max = 640.dp),
        ) {
            Text(title.uppercase(), style = MaterialTheme.typography.headlineSmall, color = Orange, textAlign = TextAlign.Center)
            if (detail != null) {
                Text(detail, style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center)
            }
            if (primary != null || secondary != null) {
                Row(horizontalArrangement = Arrangement.spacedBy(16.dp), modifier = Modifier.padding(top = 8.dp)) {
                    primary?.let { (label, onClick) ->
                        BrandButton(label, onClick, modifier = Modifier.focusRequester(focus), primary = true)
                    }
                    secondary?.let { (label, onClick) -> BrandButton(label, onClick) }
                }
            }
        }
    }
    if (primary != null) {
        LaunchedEffect(title, detail) { runCatching { focus.requestFocus() } }
    }
}
