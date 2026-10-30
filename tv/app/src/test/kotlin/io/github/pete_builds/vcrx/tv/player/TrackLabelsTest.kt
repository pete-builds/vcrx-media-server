package io.github.pete_builds.vcrx.tv.player

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class TrackLabelsTest {
    @Test
    fun languagesAndChannels() {
        assertEquals("English", TrackLabels.language("eng"))
        assertEquals("French", TrackLabels.language("fr"))
        assertEquals("French", TrackLabels.language("fre"))
        assertEquals("German", TrackLabels.language("ger"))
        assertEquals("Japanese", TrackLabels.language("jpn"))
        assertEquals("xx-unknown", TrackLabels.language("xx-unknown"))
        assertEquals("stereo", TrackLabels.channels(2))
        assertEquals("5.1", TrackLabels.channels(6))
        assertEquals("7.1", TrackLabels.channels(8))
        assertNull(TrackLabels.channels(-1))
    }
}
