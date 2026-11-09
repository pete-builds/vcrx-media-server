package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.mobile.R
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.albumCoverUrl
import io.github.pete_builds.vcrx.mobile.net.artistImageUrl
import io.github.pete_builds.vcrx.mobile.net.line
import io.github.pete_builds.vcrx.mobile.net.musicAlbum
import io.github.pete_builds.vcrx.mobile.net.musicAlbums
import io.github.pete_builds.vcrx.mobile.net.musicArtist
import io.github.pete_builds.vcrx.mobile.net.musicArtists
import io.github.pete_builds.vcrx.mobile.offline.DownloadKeys

/** Music: recently added albums, every album, and artists, from the first music library. */
@Composable
fun MusicScreen(onAlbum: (Long) -> Unit, onArtist: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    val libs by graph.libraries.collectAsState()
    val lib = libs.firstOrNull { it.kind == "music" } ?: return Message("No music library on this server.")
    var tab by rememberSaveable { mutableStateOf(0) }
    Column(Modifier.fillMaxSize()) {
        LazyRow(contentPadding = PaddingValues(16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            items(listOf("Recently added", "Albums", "Artists").withIndex().toList()) { (i, label) ->
                FilterChip(
                    selected = tab == i,
                    onClick = { tab = i },
                    label = { Text(label) },
                    colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Brand.Tint),
                )
            }
        }
        when (tab) {
            0 -> Loadable("recent${lib.id}", { graph.api.musicAlbums(lib.id, "added", null, limit = 100).albums }) { AlbumGrid(it, onAlbum) }
            1 -> Loadable("albums${lib.id}", { graph.api.musicAlbums(lib.id, "title", null, limit = 500).albums }) { AlbumGrid(it, onAlbum) }
            else -> Loadable("artists${lib.id}", { graph.api.musicArtists(lib.id, limit = 500).artists }) { artists ->
                LazyColumn(Modifier.fillMaxSize()) {
                    items(artists, key = { it.id }) { a ->
                        Row(
                            Modifier.fillMaxWidth().clickable { onArtist(a.id) }.padding(horizontal = 16.dp, vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(12.dp),
                        ) {
                            Art(if (a.hasImage) graph.api.artistImageUrl(a.id, 160) else null, Modifier.size(48.dp).clip(CircleShape))
                            Column {
                                Text(a.name, style = MaterialTheme.typography.titleMedium)
                                Text(if (a.albumCount == 1) "1 album" else "${a.albumCount} albums", color = Brand.Muted, style = MaterialTheme.typography.bodySmall)
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
fun AlbumGrid(albums: List<MusicAlbum>, onAlbum: (Long) -> Unit, withArtist: Boolean = true) {
    val graph = LocalContext.current.graph
    LazyVerticalGrid(
        columns = GridCells.Adaptive(150.dp),
        contentPadding = PaddingValues(16.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
        modifier = Modifier.fillMaxSize(),
    ) {
        items(albums, key = { it.id }) { a ->
            Column(Modifier.clickable { onAlbum(a.id) }) {
                Art(if (a.hasCover) graph.api.albumCoverUrl(a.id, 320) else null, Modifier.fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(6.dp)))
                Text(a.title, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
                Text(a.line(withArtist), style = MaterialTheme.typography.bodySmall, color = Brand.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

/** An album: cover, Play, Shuffle, Download, and its tracks. */
@Composable
fun AlbumScreen(albumId: Long, remote: MusicRemote?, onArtist: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    val me by graph.me.collectAsState()
    val online by graph.online.collectAsState()
    val downloads by graph.downloads.entries.collectAsState()
    Loadable(albumId, { graph.api.musicAlbum(albumId) }) { album ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
            item {
                Column(Modifier.fillMaxWidth().padding(16.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Art(if (album.hasCover) graph.api.albumCoverUrl(album.id, 640) else null, Modifier.widthIn(max = 320.dp).fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(8.dp)))
                    Text(album.title, style = MaterialTheme.typography.headlineSmall)
                    Text(album.artistName, color = Brand.Green, modifier = Modifier.clickable { onArtist(album.artistId) })
                    FactLine(listOf(if (album.live) album.date else album.year?.toString(), album.venue, "${album.tracks.size} tracks"))
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        BrandButton("Play", enabled = remote != null, onClick = { remote?.playAlbum(album.id) })
                        QuietButton("Shuffle", enabled = remote != null, onClick = { remote?.playAlbum(album.id, shuffle = true) })
                        if (online && graph.canDownload() && me != null) {
                            val keys = album.tracks.map { DownloadKeys.track(it.id) }
                            val have = downloads.filter { it.id in keys }
                            when {
                                have.isEmpty() -> QuietButton("Download", onClick = { graph.downloads.addAlbum(album) })
                                have.size == keys.size && have.all { it.done } -> QuietButton("Remove download", onClick = { keys.forEach(graph.downloads::remove) })
                                else -> QuietButton("Downloading ${have.count { it.done }} of ${keys.size}", onClick = {})
                            }
                        }
                    }
                }
            }
            items(album.tracks, key = { it.id }) { t ->
                Row(
                    Modifier.fillMaxWidth().clickable(enabled = remote != null) { remote?.playAlbum(album.id, t.id) }.padding(horizontal = 16.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text((t.trackNumber ?: 0).toString().padStart(2, ' '), color = Brand.Muted, modifier = Modifier.width(32.dp))
                    Column(Modifier.weight(1f)) {
                        Text(t.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (t.artistName.isNotEmpty() && t.artistName != album.artistName) Text(t.artistName, color = Brand.Muted, style = MaterialTheme.typography.bodySmall)
                    }
                    if (downloads.any { it.id == DownloadKeys.track(t.id) && it.done }) Icon(painterResource(R.drawable.ic_download), "Downloaded", tint = Brand.Green, modifier = Modifier.size(16.dp))
                    t.durationMs?.let { Text(formatTime(it), color = Brand.Muted, modifier = Modifier.padding(start = 8.dp)) }
                }
            }
        }
    }
}

/** An artist: their albums and live shows, and Play, which shuffles everything. */
@Composable
fun ArtistScreen(artistId: Long, remote: MusicRemote?, onAlbum: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    Loadable(artistId, { graph.api.musicArtist(artistId) }) { artist ->
        Column(Modifier.fillMaxSize()) {
            Row(Modifier.padding(16.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                Art(if (artist.hasImage) graph.api.artistImageUrl(artist.id, 240) else null, Modifier.size(72.dp).clip(CircleShape))
                Column(Modifier.weight(1f)) {
                    Text(artist.name, style = MaterialTheme.typography.headlineSmall)
                    Text("${artist.albumCount} albums, ${artist.trackCount} tracks", color = Brand.Muted)
                }
                BrandButton("Shuffle", enabled = remote != null, onClick = { remote?.playArtist(artist.id) })
            }
            AlbumGrid(artist.albums + artist.live + artist.appearsOn, onAlbum, withArtist = false)
        }
    }
}

/** The bar above the navigation while music plays. */
@Composable
fun MiniPlayer(remote: MusicRemote, now: NowPlaying, onOpen: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().background(Brand.Surface).clickable(onClick = onOpen).padding(horizontal = 12.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Art(now.artworkUri, Modifier.size(44.dp).clip(RoundedCornerShape(4.dp)))
        Column(Modifier.weight(1f)) {
            Text(now.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
            Text(now.artist, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall, color = Brand.Muted)
        }
        IconButton(onClick = remote::toggle) { Icon(painterResource(if (now.playing) R.drawable.ic_pause else R.drawable.ic_play), if (now.playing) "Pause" else "Play") }
        IconButton(onClick = remote::next) { Icon(painterResource(R.drawable.ic_skip_next), "Next") }
    }
}

/** Now Playing: big art, the position, and the transport. */
@Composable
fun NowPlayingScreen(remote: MusicRemote?) {
    val (now, position) = rememberNowPlaying(remote)
    if (remote == null || now == null) return Message("Nothing is playing.")
    Column(
        Modifier.fillMaxSize().padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Art(now.artworkUri, Modifier.widthIn(max = 360.dp).fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(8.dp)))
        Text(now.title, style = MaterialTheme.typography.headlineSmall, maxLines = 2)
        Text(now.artist, color = Brand.Muted)
        val duration = remote.durationMs
        Slider(
            value = if (duration > 0) (position.toFloat() / duration).coerceIn(0f, 1f) else 0f,
            onValueChange = { remote.seekTo((it * duration).toLong()) },
            colors = SliderDefaults.colors(thumbColor = Brand.Green, activeTrackColor = Brand.Green),
            modifier = Modifier.widthIn(max = 480.dp),
        )
        Row(Modifier.widthIn(max = 480.dp).fillMaxWidth()) {
            Text(formatTime(position), color = Brand.Muted)
            Spacer(Modifier.weight(1f))
            Text(formatTime(duration), color = Brand.Muted)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(24.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = remote::previous) { Icon(painterResource(R.drawable.ic_skip_previous), "Previous", Modifier.size(36.dp)) }
            IconButton(onClick = remote::toggle, modifier = Modifier.size(72.dp).clip(CircleShape).background(Brand.Tint)) {
                Icon(painterResource(if (now.playing) R.drawable.ic_pause else R.drawable.ic_play), if (now.playing) "Pause" else "Play", Modifier.size(40.dp))
            }
            IconButton(onClick = remote::next) { Icon(painterResource(R.drawable.ic_skip_next), "Next", Modifier.size(36.dp)) }
        }
    }
}
