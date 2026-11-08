package io.github.pete_builds.vcrx.mobile.auto

import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.MusicArtist
import io.github.pete_builds.vcrx.mobile.net.MusicTrack
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class BrowseTreeTest {
    @Test
    fun rootHasMusicAndAlwaysDownloads() {
        val ids = BrowseTree.root(Features(music = true)).map { it.id }
        assertEquals(listOf(BrowseTree.CONTINUE, BrowseTree.MUSIC, BrowseTree.DOWNLOADS), ids)
        assertTrue(BrowseTree.root(Features(music = true)).all { it.browsable && !it.playable })
    }

    @Test
    fun audiobooksAndPodcastsLightUpWhenTheServerHasThem() {
        val none = BrowseTree.root(Features(music = true)).map { it.id }
        assertFalse(BrowseTree.AUDIOBOOKS in none)
        assertFalse(BrowseTree.PODCASTS in none)
        val all = BrowseTree.root(Features(music = true, audiobooks = true, podcasts = true)).map { it.id }
        assertTrue(BrowseTree.AUDIOBOOKS in all)
        assertTrue(BrowseTree.PODCASTS in all)
        // No music library (an audiobook only server, or offline): Downloads stays.
        assertEquals(listOf(BrowseTree.DOWNLOADS), BrowseTree.root(Features(music = false)).map { it.id })
    }

    @Test
    fun albumsAreGridsOfPlayableFoldersAndTracksAreRows() {
        val music = BrowseTree.music()
        assertEquals(Style.Grid, music.first { it.id == BrowseTree.ALBUMS }.childStyle)
        assertEquals(Style.List, music.first { it.id == BrowseTree.ARTISTS }.childStyle)
        val album = BrowseTree.album(MusicAlbum(id = 3, title = "Live", artistName = "Band", year = 1999, hasCover = true))
        assertTrue(album.browsable && album.playable)
        assertEquals(Style.List, album.childStyle)
        assertEquals(ArtRef(ArtRef.ALBUM, 3), album.art)
        assertEquals("Band · 1999", album.subtitle)
        val noCover = BrowseTree.album(MusicAlbum(id = 4, title = "Demo"))
        assertNull(noCover.art)
        assertNull(noCover.subtitle)
        val tracks = BrowseTree.tracks(3, listOf(MusicTrack(id = 11, title = "One", albumId = 3, hasCover = true)))
        assertEquals("album:3/track:11", tracks.single().id)
        assertTrue(tracks.single().playable)
        assertFalse(tracks.single().browsable)
    }

    @Test
    fun idsReadBack() {
        assertEquals(BrowseId.Album(3), BrowseId.parse(BrowseId.album(3)))
        assertEquals(BrowseId.Artist(8), BrowseId.parse(BrowseId.artist(8)))
        assertEquals(BrowseId.AlbumTrack(3, 11), BrowseId.parse(BrowseId.albumTrack(3, 11)))
        assertEquals(BrowseId.DownloadedTrack(11), BrowseId.parse(BrowseId.downloadedTrack(11)))
        assertEquals(BrowseId.Folder(BrowseTree.ALBUMS), BrowseId.parse(BrowseTree.ALBUMS))
        assertNull(BrowseId.parse("album:x"))
        assertNull(BrowseId.parse("somewhere"))
        // Every node the tree makes can be read back.
        val nodes = BrowseTree.root(Features(music = true, audiobooks = true, podcasts = true)) + BrowseTree.music() +
            BrowseTree.artists(listOf(MusicArtist(id = 1, name = "A"))) +
            BrowseTree.downloads(listOf(DownloadedTrack(5, "T", "A", 2)))
        nodes.forEach { assertTrue(it.id, BrowseId.parse(it.id) != null) }
    }

    @Test
    fun searchMatchesEveryWord() {
        val albums = listOf(MusicAlbum(id = 1, title = "Night Drive", artistName = "Neon"), MusicAlbum(id = 2, title = "Day", artistName = "Sun"))
        val artists = listOf(MusicArtist(id = 7, name = "Neon Lights"))
        val hits = BrowseTree.search("neon", albums, artists)
        assertEquals(listOf("album:1", "artist:7"), hits.map { it.id })
        assertEquals(listOf("album:1"), BrowseTree.search("night neon", albums, artists).map { it.id })
        assertTrue(BrowseTree.search("  ", albums, artists).isEmpty())
    }

    @Test
    fun downloadsPlayFromTheDevice() {
        val d = BrowseTree.downloads(listOf(DownloadedTrack(trackId = 5, title = "T", artist = "", albumId = 2))).single()
        assertTrue(d.playable)
        assertNull(d.subtitle)
        assertEquals(BrowseNode.TYPE_TRACK, d.type)
    }
}
