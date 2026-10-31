package io.github.pete_builds.vcrx.tv.ui.music

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathBuilder
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

/** The music player's icons on the 24 unit grid, as AppIcons draws the others. */
object MusicIcons {
    val Previous: ImageVector = icon("Previous") {
        rect(6f, 6f, 2f, 12f)
        moveTo(9.5f, 12f); lineTo(18f, 6f); lineTo(18f, 18f); close()
    }

    val Next: ImageVector = icon("Next") {
        rect(16f, 6f, 2f, 12f)
        moveTo(6f, 6f); lineTo(14.5f, 12f); lineTo(6f, 18f); close()
    }

    val Shuffle: ImageVector = icon("Shuffle") {
        moveTo(10.6f, 9.2f); lineTo(5.4f, 4f); lineTo(4f, 5.4f); lineTo(9.2f, 10.6f); close()
        moveTo(14.5f, 4f); lineTo(16.5f, 6f); lineTo(4f, 18.6f); lineTo(5.4f, 20f); lineTo(18f, 7.5f); lineTo(20f, 9.5f); lineTo(20f, 4f); close()
        moveTo(14.8f, 13.4f); lineTo(13.4f, 14.8f); lineTo(16.5f, 17.9f); lineTo(14.5f, 20f); lineTo(20f, 20f); lineTo(20f, 14.5f); lineTo(18f, 16.5f); close()
    }

    val Repeat: ImageVector = icon("Repeat") {
        moveTo(7f, 7f); lineTo(17f, 7f); lineTo(17f, 10f); lineTo(21f, 6f); lineTo(17f, 2f); lineTo(17f, 5f); lineTo(5f, 5f); lineTo(5f, 11f); lineTo(7f, 11f); close()
        moveTo(17f, 17f); lineTo(7f, 17f); lineTo(7f, 14f); lineTo(3f, 18f); lineTo(7f, 22f); lineTo(7f, 19f); lineTo(19f, 19f); lineTo(19f, 13f); lineTo(17f, 13f); close()
    }

    private fun icon(name: String, block: PathBuilder.() -> Unit): ImageVector =
        ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f)
            .path(fill = SolidColor(Color.Black), pathBuilder = block)
            .build()

    private fun PathBuilder.rect(x: Float, y: Float, w: Float, h: Float) {
        moveTo(x, y); lineTo(x + w, y); lineTo(x + w, y + h); lineTo(x, y + h); close()
    }
}
