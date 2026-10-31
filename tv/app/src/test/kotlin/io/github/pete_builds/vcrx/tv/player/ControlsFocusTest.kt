package io.github.pete_builds.vcrx.tv.player

import android.view.KeyEvent
import org.junit.Assert.assertEquals
import org.junit.Test

class ControlsFocusTest {
    // Captions in two presses on a remote with no captions key: Down, then OK.
    @Test
    fun downOpensTheControlsOnSubtitles() {
        assertEquals(ControlsFocus.Subtitles, ControlsFocus.forKey(KeyEvent.KEYCODE_DPAD_DOWN))
    }

    @Test
    fun upAndEverythingElseOpenOnPlay() {
        assertEquals(ControlsFocus.Play, ControlsFocus.forKey(KeyEvent.KEYCODE_DPAD_UP))
        assertEquals(ControlsFocus.Play, ControlsFocus.forKey(KeyEvent.KEYCODE_DPAD_CENTER))
    }
}
