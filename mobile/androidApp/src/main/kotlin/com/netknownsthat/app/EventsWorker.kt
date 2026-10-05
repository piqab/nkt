package com.netknownsthat.app

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.netknownsthat.domain.model.HubEvent
import com.netknownsthat.domain.usecase.CheckNewEventsUseCase
import com.netknownsthat.domain.usecase.hostPath
import com.netknownsthat.ui.hub.EVENT_KIND
import com.netknownsthat.ui.i18n.t
import org.koin.core.component.KoinComponent
import org.koin.core.component.inject
import java.util.concurrent.TimeUnit

/**
 * Hub alerts as phone notifications: a periodic poll (WorkManager's
 * 15-minute floor is fine for "a host stopped answering"). What to show is
 * decided by the shared CheckNewEventsUseCase — the same rule iOS uses.
 */
class EventsWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params), KoinComponent {
    private val check: CheckNewEventsUseCase by inject()

    override suspend fun doWork(): Result {
        check().forEach(::post)
        return Result.success()
    }

    private fun post(e: HubEvent) {
        val ctx = applicationContext
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) return
        val intent = Intent(ctx, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
            putExtra(EXTRA_HOST_ID, e.hostId)
            putExtra(EXTRA_PATH, e.hostPath())
        }
        val pending = PendingIntent.getActivity(ctx, e.id.toInt(), intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val kind = EVENT_KIND[e.kind] ?: e.kind
        val n = NotificationCompat.Builder(ctx, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_warning)
            .setContentTitle(if (e.hostName.isNotBlank()) "${e.hostName}: $kind" else kind)
            .setContentText(e.detail)
            .setStyle(NotificationCompat.BigTextStyle().bigText(e.detail))
            .setContentIntent(pending)
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(ctx).notify(e.id.toInt(), n)
    }

    companion object {
        const val CHANNEL_ID = "hub_events"
        const val EXTRA_HOST_ID = "nkt.host_id"
        const val EXTRA_PATH = "nkt.path"
        private const val WORK_NAME = "nkt-hub-events"

        fun createChannel(context: Context) {
            val channel = NotificationChannel(CHANNEL_ID, t("Оповещения хаба", "Hub alerts"), NotificationManager.IMPORTANCE_DEFAULT)
            channel.description = t("Хост не отвечает, новые проблемы, проваленные задания", "Host not answering, new problems, failed jobs")
            context.getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        }

        fun schedule(context: Context, on: Boolean) {
            val wm = WorkManager.getInstance(context)
            if (!on) {
                wm.cancelUniqueWork(WORK_NAME)
                return
            }
            val request = PeriodicWorkRequestBuilder<EventsWorker>(15, TimeUnit.MINUTES)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .build()
            wm.enqueueUniquePeriodicWork(WORK_NAME, ExistingPeriodicWorkPolicy.KEEP, request)
        }
    }
}
