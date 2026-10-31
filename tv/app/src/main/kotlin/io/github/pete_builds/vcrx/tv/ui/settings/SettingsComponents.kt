package io.github.pete_builds.vcrx.tv.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.ui.components.BrandDialog
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label

/**
 * One setting: its name, its current value at the right, and when focused a
 * one-line description under the name, so the couch sees what a row does
 * before pressing it. A disabled row still takes focus (so the D-pad never skips
 * past it unexplained) but does nothing and is dimmed.
 */
@Composable
fun SettingRow(
    title: String,
    value: String?,
    description: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    icon: ImageVector? = null,
) {
    var focused by remember { mutableStateOf(false) }
    BrandRow(
        onClick = { if (enabled) onClick() },
        icon = icon,
        modifier = modifier.onFocusChanged { focused = it.hasFocus || it.isFocused },
    ) {
        Column(Modifier.fillMaxWidth().alpha(if (enabled) 1f else 0.5f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Label(title, style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.weight(1f))
                if (value != null) {
                    Text(
                        value,
                        style = MaterialTheme.typography.bodyMedium,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(start = 16.dp),
                    )
                }
            }
            if (focused && !description.isNullOrBlank()) {
                Text(description, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

/** An On/Off setting: pressing it flips the value. */
@Composable
fun ToggleRow(
    title: String,
    checked: Boolean,
    description: String?,
    onChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    SettingRow(title, if (checked) "On" else "Off", description, onClick = { onChange(!checked) }, modifier, enabled)
}

/** A setting with a few named values: pressing it opens a picker with the current one focused. */
@Composable
fun <T> ChoiceRow(
    title: String,
    current: T,
    options: List<T>,
    label: (T) -> String,
    description: String?,
    onPick: (T) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    var open by remember { mutableStateOf(false) }
    SettingRow(title, label(current), description, onClick = { open = true }, modifier, enabled)
    if (open) {
        ChoiceDialog(title, current, options, label, onPick = { open = false; onPick(it) }, onDismiss = { open = false })
    }
}

@Composable
fun <T> ChoiceDialog(title: String, current: T, options: List<T>, label: (T) -> String, onPick: (T) -> Unit, onDismiss: () -> Unit) {
    val focus = remember { FocusRequester() }
    val focusIndex = options.indexOf(current).coerceAtLeast(0)
    BrandDialog(title, onDismiss = onDismiss) {
        options.forEachIndexed { index, option ->
            BrandRow(
                onClick = { onPick(option) },
                selected = option == current,
                modifier = if (index == focusIndex) Modifier.focusRequester(focus) else Modifier,
            ) {
                Label(label(option))
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
}

/** A heading between groups of rows. */
@Composable
fun SectionLabel(text: String) {
    Label(text, style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(start = 16.dp, top = 20.dp, bottom = 4.dp))
}

/** A read-only fact in a page: a label and its text. */
@Composable
fun FactRow(label: String, text: String, modifier: Modifier = Modifier) {
    BrandRow(onClick = { }, modifier = modifier) {
        Column(Modifier.width(760.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Label(label, style = MaterialTheme.typography.labelMedium)
            Text(text, style = MaterialTheme.typography.bodyLarge)
        }
    }
}
