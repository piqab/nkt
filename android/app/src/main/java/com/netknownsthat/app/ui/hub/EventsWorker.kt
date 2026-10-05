package com.netknownsthat.app.ui.hub

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
import com.netknownsthat.app.MainActivity
import com.netknownsthat.app.NktApplication
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.HubEventsResponse
import java.util.concurrent.TimeUnit
import com.netknownsthat.app.i18n.t

/**
 * Hub alerts as phone notifications. A periodic poll rather than push: the
 * hub has no push channel to a phone (Firebase would mean a Google account
 * and a server key on the hub), and WorkManager's 15-minute floor is fine
 * for "a host stopped answering".
 *
 * Which kinds notify is the hub's own choice (the `notify` map of
 * /hub/events, set in «Оповещения → Настройки») — the phone does not keep a
 * second list that could disagree with Slack/Telegram.
 */
class EventsWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val app = applicationContext as NktApplication
        val settings = app.settingsStore
        if (!settings.eventsNotify()) return Result.success()
        val client = app.hubClient
        if (!client.ensureBootstrapped()) return Result.success()
        val lastId = settings.eventsLastId()
        // Hub-level call: never scoped to whatever host the UI has open.
        val r = client.get<HubEventsResponse>("/hub/events?limit=50&after=$lastId")
        if (r !is HubClient.ApiResult.Success) {
            // Expired session or hub away: nothing to show, try next period.
            return Result.success()
        }
        val events = r.value.events
        val newest = events.maxOfOrNull { it.id } ?: return Result.success()
        // The very first run only takes note of where the journal is: a
        // phone switched on now must not replay the hub's whole history.
        if (lastId > 0) {
            events.filter { r.value.notify[it.kind] == true }
                .sortedBy { it.id }
                .takeLast(MAX_PER_RUN)
                .forEach { e -> post(e.id, e.hostName, EVENT_KIND[e.kind] ?: e.kind, e.detail, e.hostId, eventPath(e)) }
        }
        settings.setEventsLastId(newest)
        return Result.success()
    }

    private fun post(id: Long, host: String, kind: String, detail: String, hostId: Long, path: String) {
        val ctx = applicationContext
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) return
        val intent = Intent(ctx, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
            putExtra(EXTRA_HOST_ID, hostId)
            putExtra(EXTRA_PATH, path)
        }
        val pending = PendingIntent.getActivity(
            ctx, id.toInt(), intent, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val n = NotificationCompat.Builder(ctx, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_warning)
            .setContentTitle(if (host.isNotBlank()) "$host: $kind" else kind)
            .setContentText(detail)
            .setStyle(NotificationCompat.BigTextStyle().bigText(detail))
            .setContentIntent(pending)
            .setAutoCancel(true)
            .build()
        NotificationManagerCompat.from(ctx).notify(id.toInt(), n)
    }

    companion object {
        const val CHANNEL_ID = "hub_events"
        const val EXTRA_HOST_ID = "nkt.host_id"
        const val EXTRA_PATH = "nkt.path"
        private const val WORK_NAME = "nkt-hub-events"
        private const val MAX_PER_RUN = 10

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
