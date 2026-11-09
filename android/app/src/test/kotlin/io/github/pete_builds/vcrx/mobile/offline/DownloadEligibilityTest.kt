package io.github.pete_builds.vcrx.mobile.offline

import io.github.pete_builds.vcrx.mobile.net.Item
import io.github.pete_builds.vcrx.mobile.net.User
import io.github.pete_builds.vcrx.mobile.net.Version
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DownloadEligibilityTest {
    private fun user(kind: String) = User(id = 1, username = "a", kind = kind)
    private val movie = Item(id = 5, kind = "movie", title = "A film", versions = listOf(Version(id = 9), Version(id = 10)))

    @Test
    fun accountsAndProfilesMayDownload() {
        assertTrue(DownloadEligibility.allowed(user("user"), invitedDevice = false))
        assertTrue(DownloadEligibility.allowed(user("profile"), invitedDevice = false))
        assertTrue(DownloadEligibility.canDownload(movie, user("user"), invitedDevice = false))
    }

    @Test
    fun anInvitedDeviceGetsNoDownloads() {
        assertFalse(DownloadEligibility.allowed(user("token"), invitedDevice = false))
        assertFalse(DownloadEligibility.canDownload(movie, user("token"), invitedDevice = false))
        // Known locally even before /me answers, or from an older server without kind.
        assertFalse(DownloadEligibility.allowed(user(""), invitedDevice = true))
        assertFalse(DownloadEligibility.allowed(user("user"), invitedDevice = true))
    }

    @Test
    fun unknownSessionGetsNoDownloads() {
        assertFalse(DownloadEligibility.allowed(null, invitedDevice = false))
    }

    @Test
    fun onlyMoviesAndEpisodesWithAFile() {
        val u = user("user")
        assertTrue(DownloadEligibility.canDownload(movie.copy(kind = "episode"), u, false))
        assertFalse(DownloadEligibility.canDownload(movie.copy(kind = "show"), u, false))
        assertFalse(DownloadEligibility.canDownload(movie.copy(kind = "season"), u, false))
        assertFalse(DownloadEligibility.canDownload(movie.copy(versions = emptyList()), u, false))
        assertEquals(9L, DownloadEligibility.versionToKeep(movie))
        assertNull(DownloadEligibility.versionToKeep(movie.copy(versions = emptyList())))
    }

    @Test
    fun keysNameTheContentAndMapBackToTheirRoute() {
        assertEquals("version:9", DownloadKeys.version(9))
        assertEquals("/api/v1/versions/9/file", DownloadKeys.pathFor("version:9"))
        assertEquals("/api/v1/music/tracks/4/file", DownloadKeys.pathFor(DownloadKeys.track(4)))
        assertTrue(DownloadKeys.isAudio("track:4"))
        assertFalse(DownloadKeys.isAudio("version:4"))
        assertNull(DownloadKeys.pathFor("http://elsewhere/file"))
        assertNull(DownloadKeys.pathFor("version:x"))
    }
}
