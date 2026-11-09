package io.github.pete_builds.vcrx.mobile.offline

import android.app.Notification
import android.app.PendingIntent
import android.content.Intent
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.offline.Download
import androidx.media3.exoplayer.offline.DownloadManager
import androidx.media3.exoplayer.offline.DownloadNotificationHelper
import androidx.media3.exoplayer.offline.DownloadService
import androidx.media3.exoplayer.scheduler.Scheduler
import io.github.pete_builds.vcrx.mobile.R
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.ui.MainActivity

/**
 * Runs downloads in the foreground with a progress notification. No scheduler:
 * downloads waiting for Wi-Fi carry on the next time the app or this service
 * runs, rather than waking the phone by themselves.
 */
@OptIn(UnstableApi::class)
class VcrxDownloadService : DownloadService(
    NOTIFICATION_ID,
    DEFAULT_FOREGROUND_NOTIFICATION_UPDATE_INTERVAL,
    CHANNEL_ID,
    R.string.downloads_channel,
    R.string.downloads_channel_description,
) {
    private val helper by lazy { DownloadNotificationHelper(this, CHANNEL_ID) }

    override fun getDownloadManager(): DownloadManager = graph.downloads.manager

    override fun getScheduler(): Scheduler? = null

    override fun getForegroundNotification(downloads: MutableList<Download>, notMetRequirements: Int): Notification {
        val open = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java).putExtra(MainActivity.EXTRA_OPEN, MainActivity.OPEN_DOWNLOADS),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return helper.buildProgressNotification(this, R.drawable.ic_download, open, null, downloads, notMetRequirements)
    }

    private companion object {
        const val NOTIFICATION_ID = 7301
        const val CHANNEL_ID = "downloads"
    }
}
