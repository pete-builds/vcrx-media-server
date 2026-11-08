package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.mobile.R
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.Item
import io.github.pete_builds.vcrx.mobile.net.ServerApi
import kotlinx.coroutines.delay

fun ServerApi.cardArt(item: Item, width: Int = 342): String? = when {
    item.kind == "episode" && item.hasThumb -> artworkUrl(item.id, "thumb", width * 2)
    item.hasPoster -> artworkUrl(item.id, "poster", width)
    item.showId != null -> artworkUrl(item.showId, "poster", width)
    else -> null
}

/** Home: the rows the server lays out (Continue Watching, recently added, and the rest), as on every client. */
@Composable
fun HomeScreen(onItem: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    Loadable(Unit, { graph.api.home() }) { rows ->
        if (rows.isEmpty()) return@Loadable Message("Nothing here yet. Add a library on the server.")
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
            items(rows, key = { it.id }) { row ->
                Column {
                    Text(row.title, style = SectionTitle, modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp))
                    val wide = row.items.all { it.kind == "episode" }
                    LazyRow(contentPadding = PaddingValues(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        items(row.items, key = { it.id }) { item ->
                            PosterCard(item, graph.api.cardArt(item), onClick = { onItem(item.id) }, width = if (wide) 220.dp else 120.dp, wide = wide)
                        }
                    }
                }
            }
        }
    }
}

/** Every library this session may see; music opens the music pages. */
@Composable
fun LibrariesScreen(onLibrary: (Long, String) -> Unit, onMusic: () -> Unit, onOther: (String) -> Unit) {
    val graph = LocalContext.current.graph
    Loadable(Unit, { graph.api.libraries() }) { libs ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
            items(libs, key = { it.id }) { lib ->
                ListItem(
                    headlineContent = { Text(lib.name, style = MaterialTheme.typography.titleMedium) },
                    supportingContent = { Text("${lib.itemCount} titles", color = Brand.Muted) },
                    leadingContent = { Icon(painterResource(if (lib.kind == "music") R.drawable.ic_music else R.drawable.ic_library), null, tint = Brand.Green) },
                    colors = ListItemDefaults.colors(containerColor = Brand.Black),
                    modifier = Modifier.clickable {
                        when (lib.kind) {
                            "music" -> onMusic()
                            "movie", "show" -> onLibrary(lib.id, lib.name)
                            // Audiobooks and podcasts get their own pages once the server's routes are final.
                            else -> onOther(lib.name)
                        }
                    },
                )
            }
        }
    }
}

@Composable
fun ItemGrid(items: List<Item>, onItem: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    LazyVerticalGrid(
        columns = GridCells.Adaptive(112.dp),
        contentPadding = PaddingValues(16.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
        modifier = Modifier.fillMaxSize(),
    ) {
        items(items, key = { it.id }) { item -> PosterCard(item, graph.api.cardArt(item), onClick = { onItem(item.id) }, width = 112.dp) }
    }
}

/** One library's titles, A to Z. */
@Composable
fun LibraryScreen(libraryId: Long, onItem: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    Loadable(libraryId, { graph.api.libraryItems(libraryId, "title", "asc", 500, 0).items }) { items ->
        if (items.isEmpty()) Message("This library is empty.") else ItemGrid(items, onItem)
    }
}

/** Search the libraries; answers as you type. */
@Composable
fun SearchScreen(onItem: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    var query by rememberSaveable { mutableStateOf("") }
    var results by remember { mutableStateOf<List<Item>>(emptyList()) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(query) {
        if (query.isBlank()) {
            results = emptyList()
            return@LaunchedEffect
        }
        delay(350)
        try {
            results = graph.api.search(query.trim())
            error = null
        } catch (e: ApiException) {
            error = e.message
        }
    }
    Column(Modifier.fillMaxSize()) {
        OutlinedTextField(
            query,
            { query = it },
            leadingIcon = { Icon(Icons.Filled.Search, null) },
            placeholder = { Text("Search titles") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth().padding(16.dp),
        )
        error?.let { Text(it, color = Brand.Error, modifier = Modifier.padding(horizontal = 16.dp)) }
        when {
            query.isBlank() -> Message("Movies, shows and episodes")
            results.isEmpty() -> Message("No matches yet")
            else -> ItemGrid(results, onItem)
        }
    }
}

@Composable
fun FactLine(parts: List<String?>) {
    val text = parts.filterNotNull().filter { it.isNotBlank() }.joinToString(" · ")
    if (text.isNotEmpty()) Text(text, color = Brand.Muted, style = MaterialTheme.typography.bodyMedium)
}

@Composable
fun RowHeader(text: String) = Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
    Text(text, style = SectionTitle)
}

@Composable
fun FillBox(content: @Composable () -> Unit) = Box(Modifier.fillMaxSize()) { content() }
