package io.github.pete_builds.vcrx.mobile.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class NavTabsTest {
    @Test
    fun sixFitAsBefore() {
        val six = listOf("home", "search", "libraries", "music", "iptv", "downloads")
        assertEquals(six to emptyList<String>(), NavTabs.split(six))
    }

    @Test
    fun moreTakesTheMiddleAndDownloadsStays() {
        val all = listOf("home", "search", "libraries", "music", "audiobooks", "podcasts", "iptv", "downloads")
        val (bar, more) = NavTabs.split(all)
        assertEquals(listOf("home", "search", "libraries", "music", NavTabs.MORE, "downloads"), bar)
        assertEquals(listOf("audiobooks", "podcasts", "iptv"), more)
        assertEquals(NavTabs.MAX_BAR, bar.size)
        // Every destination is reachable, once.
        assertEquals(all.toSet(), (bar - NavTabs.MORE + more).toSet())
    }
}
