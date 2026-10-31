package io.github.pete_builds.vcrx.tv.player

import android.graphics.Color
import android.graphics.Typeface
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.CaptionStyleCompat
import androidx.media3.ui.SubtitleView
import io.github.pete_builds.vcrx.tv.settings.SubtitleEdge
import io.github.pete_builds.vcrx.tv.settings.SubtitleStyle

/**
 * Applies Settings > Subtitles > Style to a Media3 [SubtitleView], the one the
 * player draws into and the live preview alike. Not custom: the TV's own caption
 * settings, which is where viewers with low vision may already have set them up.
 */
@OptIn(UnstableApi::class)
object CaptionStyles {
    fun apply(view: SubtitleView, style: SubtitleStyle) {
        if (!style.custom) {
            view.setUserDefaultStyle()
            view.setUserDefaultTextSize()
            view.setApplyEmbeddedStyles(true)
            view.setApplyEmbeddedFontSizes(true)
            view.setBottomPaddingFraction(SubtitleView.DEFAULT_BOTTOM_PADDING_FRACTION)
            return
        }
        view.setStyle(
            CaptionStyleCompat(
                style.color.argb.toInt(),
                Color.argb(style.background.alpha, 0, 0, 0),
                Color.TRANSPARENT,
                edgeType(style.edge),
                Color.BLACK,
                Typeface.DEFAULT,
            ),
        )
        // Chosen size and colour win over what the file asks for; positions and italics still apply.
        view.setApplyEmbeddedStyles(true)
        view.setApplyEmbeddedFontSizes(false)
        view.setFractionalTextSize(SubtitleView.DEFAULT_TEXT_SIZE_FRACTION * style.size.scale)
        view.setBottomPaddingFraction(style.position.bottomFraction)
    }

    private fun edgeType(edge: SubtitleEdge): Int = when (edge) {
        SubtitleEdge.None -> CaptionStyleCompat.EDGE_TYPE_NONE
        SubtitleEdge.Outline -> CaptionStyleCompat.EDGE_TYPE_OUTLINE
        SubtitleEdge.DropShadow -> CaptionStyleCompat.EDGE_TYPE_DROP_SHADOW
        SubtitleEdge.Raised -> CaptionStyleCompat.EDGE_TYPE_RAISED
        SubtitleEdge.Depressed -> CaptionStyleCompat.EDGE_TYPE_DEPRESSED
    }
}
