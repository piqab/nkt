package com.netknownsthat.domain.usecase

import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.HubEvent
import com.netknownsthat.domain.repository.EventsRepository
import com.netknownsthat.domain.repository.SessionRepository
import com.netknownsthat.domain.repository.SettingsRepository

/**
 * New hub events to show as phone notifications. Which kinds notify is the
 * hub's own choice (the `notify` map, set in its alert settings) — the phone
 * keeps no second list that could disagree with Slack/Telegram. The very
 * first check only notes where the journal is, so a phone switched on now
 * does not replay the hub's whole history.
 */
class CheckNewEventsUseCase(
    private val session: SessionRepository,
    private val settings: SettingsRepository,
    private val events: EventsRepository,
    private val maxPerRun: Int = 10,
) {
    suspend operator fun invoke(): List<HubEvent> {
        if (!settings.eventNotifications.value) return emptyList()
        if (!session.restore()) return emptyList()
        val lastId = settings.lastNotifiedEventId()
        val r = events.events(limit = 50, after = lastId)
        if (r !is Outcome.Success) return emptyList()
        val newest = r.value.events.maxOfOrNull { it.id } ?: return emptyList()
        settings.setLastNotifiedEventId(newest)
        if (lastId == 0L) return emptyList()
        return r.value.events.filter { r.value.notify[it.kind] == true }
            .sortedBy { it.id }
            .takeLast(maxPerRun)
    }
}

/** Kinds without a link of their own open this host section (web's KIND_LINK). */
private val KIND_LINK = mapOf(
    "problems" to "/findings",
    "resolved" to "/findings",
    "bans" to "/fail2ban",
    "job-failed" to "/jobs",
    "forecast" to "/usage",
)

/** Host section path an event leads to: its own link, else by kind. */
fun HubEvent.hostPath(): String = link.ifBlank { KIND_LINK[kind] ?: "/" }
