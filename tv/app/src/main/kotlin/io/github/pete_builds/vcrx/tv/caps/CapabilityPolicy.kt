package io.github.pete_builds.vcrx.tv.caps

import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.settings.Preferences

/**
 * Narrows what the probe found by what the viewer chose in Settings, before the
 * capabilities go to the server. It only ever removes or lowers, never adds, so
 * no setting can make the server send something this TV cannot play. Pure, so
 * each setting's effect is unit tested.
 */
object CapabilityPolicy {
    fun apply(caps: Capabilities, prefs: Preferences): Capabilities {
        var out = caps
        if (!prefs.audio.passthrough) {
            out = out.copy(audioCodecs = out.audioCodecs.filter { it.decoder != CapabilityBuilder.PASSTHROUGH })
        }
        if (!prefs.video.hdr) out = out.copy(hdrTypes = out.hdrTypes.filter { it !in HDR })
        if (!prefs.video.dolbyVision) out = out.copy(hdrTypes = out.hdrTypes.filter { it != CodecNames.DOLBY_VISION })
        val cap = prefs.playback.quality
        // The server checks each decoder's own size limit before the device's, so both are capped.
        cap.width?.let { w ->
            out = out.copy(
                maxWidth = minOrCap(out.maxWidth, w),
                videoCodecs = out.videoCodecs.map { it.copy(maxWidth = minOrCap(it.maxWidth, w)) },
            )
        }
        cap.height?.let { h ->
            out = out.copy(
                maxHeight = minOrCap(out.maxHeight, h),
                videoCodecs = out.videoCodecs.map { it.copy(maxHeight = minOrCap(it.maxHeight, h)) },
            )
        }
        cap.bitrate?.let { b -> out = out.copy(maxBitrate = out.maxBitrate?.let { minOf(it, b) } ?: b) }
        return out
    }

    private fun minOrCap(current: Int?, cap: Int): Int = current?.takeIf { it > 0 }?.let { minOf(it, cap) } ?: cap

    /** The HDR formats the HDR switch covers; Dolby Vision has its own. */
    private val HDR = setOf(CodecNames.HDR10, CodecNames.HDR10_PLUS, CodecNames.HLG)
}
