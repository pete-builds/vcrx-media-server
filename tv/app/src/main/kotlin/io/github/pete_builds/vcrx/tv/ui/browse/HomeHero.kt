package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.compose.foundation.background
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.episodeCode
import io.github.pete_builds.vcrx.tv.ui.details.formatRuntime
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.White

/** How tall the hero stands; the rows scroll beneath it. */
val HERO_HEIGHT = 250.dp

/**
 * The top of Home, as on the web and in Plex: a title's backdrop fading into
 * black, its name, details and plot, and Play and More info. It shows the
 * day's featured title ([featured] true) until focus moves into the rows, then
 * whichever card has focus, so the rows need no titles under their posters.
 */
@Composable
fun HomeHero(item: Item, featured: Boolean, firstButton: FocusRequester, modifier: Modifier = Modifier) {
    val api = LocalContext.current.graph.api
    val actions = LocalAppActions.current
    Box(modifier.fillMaxWidth().height(HERO_HEIGHT)) {
        // An episode borrows its show's backdrop.
        val artId = if (item.kind == "episode") item.showId ?: item.id else item.id
        val hasArt = item.hasBackdrop || item.kind == "episode"
        val url = if (hasArt) api.artworkUrl(artId, "backdrop", 1280) else null
        if (url != null) {
            AsyncImage(model = url, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        }
        Box(Modifier.fillMaxSize().background(Brush.horizontalGradient(0f to Black, 0.45f to Black.copy(alpha = 0.75f), 1f to Color.Transparent)))
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.55f to Color.Transparent, 1f to Black)))
        Column(
            Modifier.align(Alignment.CenterStart).padding(start = 24.dp, top = 16.dp).width(440.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (featured) {
                Label(if (item.kind == "show") "Featured show" else "Featured movie", style = MaterialTheme.typography.labelMedium, color = Gray)
            }
            item.showTitle?.takeIf { item.kind == "episode" }?.let {
                Label("$it  ${episodeCode(item).orEmpty()}", style = MaterialTheme.typography.labelMedium, color = Gray)
            }
            Text(item.title, style = MaterialTheme.typography.headlineMedium, color = White, maxLines = 2, overflow = TextOverflow.Ellipsis)
            heroMeta(item).takeIf { it.isNotEmpty() }?.let { Label(it, style = MaterialTheme.typography.labelMedium, color = Gray) }
            (item.overview ?: item.tagline)?.takeIf { it.isNotBlank() }?.let {
                Text(it, style = MaterialTheme.typography.bodyMedium, color = White, maxLines = 3, overflow = TextOverflow.Ellipsis)
            }
            Row(
                Modifier.padding(top = 6.dp).focusRestorer(firstButton).focusGroup(),
                horizontalArrangement = Arrangement.spacedBy(14.dp),
            ) {
                val play = heroPlay(item)
                if (play != null) {
                    BrandButton(
                        play.label,
                        onClick = { actions.play(play.itemId, play.title, null, fromStart = !play.resume, resume = play.resume) },
                        primary = true,
                        modifier = Modifier.focusRequester(firstButton),
                    )
                }
                BrandButton(
                    "More info",
                    onClick = { actions.openDetails(item.id) },
                    primary = play == null,
                    modifier = if (play == null) Modifier.focusRequester(firstButton) else Modifier,
                )
            }
        }
    }
}

/** What the hero's Play button starts, or null when there is nothing to play from here. */
data class HeroPlay(val itemId: Long, val title: String, val label: String, val resume: Boolean)

fun heroPlay(item: Item): HeroPlay? = when (item.kind) {
    "movie", "episode" -> {
        val resume = (item.watch?.positionMs ?: 0) > 0 && item.watch?.played != true
        val title = if (item.kind == "episode") listOfNotNull(item.showTitle, episodeCode(item), item.title).joinToString("  ") else item.title
        HeroPlay(item.id, title, if (resume) "Resume" else "Play", resume)
    }
    "show" -> item.nextEpisode?.let { n ->
        val resume = (n.watch?.positionMs ?: 0) > 0 && n.watch?.played != true
        val code = episodeCode(n)
        HeroPlay(n.id, listOfNotNull(item.title, code, n.title).joinToString("  "), listOfNotNull(if (resume) "Resume" else "Play", code).joinToString(" "), resume)
    }
    else -> null
}

/** Year, rating, runtime or seasons, resolution and score, as the web's hero chips. */
fun heroMeta(item: Item): String = listOfNotNull(
    item.year?.toString(),
    item.contentRating?.takeIf { it.isNotBlank() },
    item.runtimeMs?.takeIf { item.kind != "show" }?.let { formatRuntime(it) },
    item.childCount?.takeIf { item.kind == "show" && it > 0 }?.let { "$it season${if (it == 1) "" else "s"}" },
    item.resolution,
    (item.ratings["imdb"] ?: item.ratings["tmdb"])?.let { "★ %.1f".format(java.util.Locale.US, it) },
).joinToString("   ")
