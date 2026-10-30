package io.github.pete_builds.vcrx.tv.ui.components

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathBuilder
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

/**
 * The few icons material-icons-core lacks, drawn on its 24 unit grid. Kept here
 * rather than pulling in the much larger extended icon set.
 */
object AppIcons {
    val Pause: ImageVector = icon("Pause") {
        rect(6f, 5f, 4f, 14f)
        rect(14f, 5f, 4f, 14f)
    }

    val Rewind: ImageVector = icon("Rewind") {
        moveTo(11f, 6f); lineTo(11f, 18f); lineTo(2.5f, 12f); close()
        moveTo(20.5f, 6f); lineTo(20.5f, 18f); lineTo(12f, 12f); close()
    }

    val Forward: ImageVector = icon("Forward") {
        moveTo(13f, 6f); lineTo(13f, 18f); lineTo(21.5f, 12f); close()
        moveTo(3.5f, 6f); lineTo(3.5f, 18f); lineTo(12f, 12f); close()
    }

    val Subtitles: ImageVector = icon("Subtitles", evenOdd = true) {
        rect(2f, 4f, 20f, 16f)
        rect(5f, 12f, 4f, 2f)
        rect(11f, 12f, 8f, 2f)
        rect(5f, 15f, 10f, 2f)
        rect(17f, 15f, 2f, 2f)
    }

    val Audio: ImageVector = icon("Audio") {
        moveTo(3f, 9f); lineTo(7f, 9f); lineTo(12f, 4f); lineTo(12f, 20f); lineTo(7f, 15f); lineTo(3f, 15f); close()
        rect(14.5f, 8.5f, 2f, 7f)
        rect(18.5f, 5.5f, 2f, 13f)
    }

    val Movie: ImageVector = icon("Movie", evenOdd = true) {
        rect(3f, 4f, 18f, 16f)
        rect(5f, 6f, 2f, 2f); rect(5f, 11f, 2f, 2f); rect(5f, 16f, 2f, 2f)
        rect(17f, 6f, 2f, 2f); rect(17f, 11f, 2f, 2f); rect(17f, 16f, 2f, 2f)
        rect(9f, 6f, 6f, 12f)
    }

    val Tv: ImageVector = icon("Tv", evenOdd = true) {
        rect(2f, 4f, 20f, 13f)
        rect(4f, 6f, 16f, 9f)
        rect(8f, 18f, 8f, 2f)
    }

    private fun icon(name: String, evenOdd: Boolean = false, block: PathBuilder.() -> Unit): ImageVector =
        ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f)
            .path(
                fill = SolidColor(Color.Black),
                pathFillType = if (evenOdd) PathFillType.EvenOdd else PathFillType.NonZero,
                pathBuilder = block,
            )
            .build()

    private fun PathBuilder.rect(x: Float, y: Float, w: Float, h: Float) {
        moveTo(x, y); lineTo(x + w, y); lineTo(x + w, y + h); lineTo(x, y + h); close()
    }
}
