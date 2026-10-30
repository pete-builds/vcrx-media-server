package io.github.pete_builds.vcrx.tv.data

import android.content.Context
import android.os.Build
import android.provider.Settings
import io.github.pete_builds.vcrx.tv.BuildConfig
import io.github.pete_builds.vcrx.tv.net.Device

/** Who is asking, as sent with login. The server records it in activity history. */
object DeviceInfo {
    const val PLATFORM = "android_tv"
    /** Wire value for Device.client in the contract, not a display name. */
    const val CLIENT = "vcrx-tv"

    fun device(context: Context, settings: SettingsStore): Device {
        val model = modelName(Build.MANUFACTURER, Build.MODEL)
        val name = Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME)
            ?.takeIf { it.isNotBlank() } ?: model
        return Device(
            id = settings.deviceId,
            name = name,
            model = model,
            platform = PLATFORM,
            client = CLIENT,
            clientVersion = BuildConfig.VERSION_NAME,
        )
    }

    /**
     * The contract has one model field, so the manufacturer goes in front of the model
     * ("TCL 55S546"), unless the model already starts with it.
     */
    fun modelName(manufacturer: String?, model: String?): String {
        val maker = manufacturer?.trim().orEmpty()
        val product = model?.trim().orEmpty()
        return when {
            product.isEmpty() -> maker.ifEmpty { "Android TV" }
            maker.isEmpty() || product.startsWith(maker, ignoreCase = true) -> product
            else -> "$maker $product"
        }
    }
}
