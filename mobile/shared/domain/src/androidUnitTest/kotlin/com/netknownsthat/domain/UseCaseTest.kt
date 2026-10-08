package com.netknownsthat.domain

import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.HubEvent
import com.netknownsthat.domain.model.HubEventsResponse
import com.netknownsthat.domain.model.JobLogLine
import com.netknownsthat.domain.model.JobLogResponse
import com.netknownsthat.domain.model.JobRecord
import com.netknownsthat.domain.model.JobsResponse
import com.netknownsthat.domain.model.K8sUsageCluster
import com.netknownsthat.domain.model.OutagesResponse
import com.netknownsthat.domain.model.TargetsResponse
import com.netknownsthat.domain.model.UsageResponse
import com.netknownsthat.domain.model.UsageTopEntry
import com.netknownsthat.domain.model.UsageTopResponse
import com.netknownsthat.domain.repository.EventsRepository
import com.netknownsthat.domain.repository.HostMonitoringRepository
import com.netknownsthat.domain.repository.JobsRepository
import com.netknownsthat.domain.repository.TerminalMode
import com.netknownsthat.domain.usecase.ActionResult
import com.netknownsthat.domain.usecase.AwaitSettledUseCase
import com.netknownsthat.domain.usecase.HostSectionKey
import com.netknownsthat.domain.usecase.LoadUsageUseCase
import com.netknownsthat.domain.usecase.OpenTerminalUseCase
import com.netknownsthat.domain.usecase.UsageQuery
import com.netknownsthat.domain.usecase.WatchJobUseCase
import com.netknownsthat.domain.usecase.CheckNewEventsUseCase
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.runTest
import com.netknownsthat.domain.repository.UiScale
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.time.Duration.Companion.milliseconds

/** Domain logic against fake repositories (the domain knows no HTTP). */
class UseCaseTest {
    private val host = HostTarget(3)

    private class FakeMonitoring(
        val sources: List<String>,
        val top: List<UsageTopEntry>,
        val cluster: K8sUsageCluster = K8sUsageCluster(),
    ) : HostMonitoringRepository {
        val seriesAsked = mutableListOf<List<String>>()
        override suspend fun targets(host: HostTarget) = Outcome.Success(TargetsResponse())
        override suspend fun outages(host: HostTarget) = Outcome.Success(OutagesResponse())
        override suspend fun checkTarget(host: HostTarget, targetId: Long) = Outcome.Success(Unit)
        override suspend fun targetHistory(host: HostTarget, targetId: Long, since: String, tzMinutes: Int) = Outcome.Success(emptyList<com.netknownsthat.domain.model.AvailabilityBucket>())
        override suspend fun addTarget(host: HostTarget, target: com.netknownsthat.domain.model.NewTarget) = Outcome.Success(Unit)
        override suspend fun setTargetEnabled(host: HostTarget, targetId: Long, enabled: Boolean) = Outcome.Success(Unit)
        override suspend fun deleteTarget(host: HostTarget, targetId: Long) = Outcome.Success(Unit)
        override suspend fun usageSources(host: HostTarget) = Outcome.Success(sources)
        override suspend fun usageTop(host: HostTarget, source: String, metric: String, limit: Int) = Outcome.Success(UsageTopResponse(metric, source, top))
        override suspend fun usageSeries(host: HostTarget, source: String, metric: String, agg: String, subjects: List<String>): Outcome<UsageResponse> {
            seriesAsked += subjects
            return Outcome.Success(UsageResponse())
        }
        override suspend fun k8sCluster(host: HostTarget) = Outcome.Success(cluster)
        override suspend fun collectorJobs(host: HostTarget) = Outcome.Success(JobsResponse())
    }

    @Test
    fun usageOpensFirstAvailableSourceNotDocker() = runTest {
        val repo = FakeMonitoring(listOf("host", "k8s_node"), listOf(UsageTopEntry("lab-w-1", 10.0, 1)))
        val r = (LoadUsageUseCase(repo)(host, UsageQuery()) as Outcome.Success).value
        assertEquals("k8s_node", r.source)
        assertEquals(listOf("k8s_node", "host"), r.sources)
    }

    @Test
    fun usageFiltersPodsByNodeAndNamespace() = runTest {
        val top = listOf(
            UsageTopEntry("shop/api-1", 5.0, 1),
            UsageTopEntry("shop/api-2", 4.0, 1),
            UsageTopEntry("kube-system/dns", 3.0, 1),
        )
        val cluster = K8sUsageCluster(pods = mapOf("shop/api-1" to "lab-w-1", "shop/api-2" to "lab-cp-1", "kube-system/dns" to "lab-w-1"))
        val repo = FakeMonitoring(listOf("k8s"), top, cluster)
        val byNode = (LoadUsageUseCase(repo)(host, UsageQuery(source = "k8s", node = "lab-w-1")) as Outcome.Success).value
        assertEquals(listOf("shop/api-1", "kube-system/dns"), byNode.top.map { it.subject })
        val byNs = (LoadUsageUseCase(repo)(host, UsageQuery(source = "k8s", node = "lab-w-1", namespace = "shop")) as Outcome.Success).value
        assertEquals(listOf("shop/api-1"), byNs.top.map { it.subject })
    }

    @Test
    fun hostHasNoNetworkSeries() = runTest {
        val repo = FakeMonitoring(listOf("host"), listOf(UsageTopEntry("host", 1.0, 1)))
        val r = (LoadUsageUseCase(repo)(host, UsageQuery(source = "host", metric = "net_rx_bytes")) as Outcome.Success).value
        assertEquals("cpu_pct", r.metric)
    }

    @Test
    fun awaitSettledWaitsForTheState() = runTest {
        var calls = 0
        val r = AwaitSettledUseCase(poll = 1.milliseconds)(
            action = { Outcome.Success(Unit) },
            fetch = { Outcome.Success(++calls) },
            onFetched = {},
            settled = { it >= 3 },
        )
        assertEquals(ActionResult.Settled, r)
        assertEquals(3, calls)
    }

    @Test
    fun awaitSettledReportsFailure() = runTest {
        val r = AwaitSettledUseCase()(
            action = { Outcome.Failure(AppError.Server(409, "busy")) },
            fetch = { Outcome.Success(0) },
            onFetched = {},
            settled = { true },
        )
        assertEquals(ActionResult.Failed(AppError.Server(409, "busy")), r)
    }

    @Test
    fun watchJobFollowsUntilFinished() = runTest {
        val repo = object : JobsRepository {
            var step = 0
            override suspend fun jobs(owner: JobOwner, statusFilter: String, limit: Int) = throw UnsupportedOperationException()
            override suspend fun log(owner: JobOwner, jobId: Long, after: Long): Outcome<JobLogResponse> {
                step++
                return Outcome.Success(
                    when (step) {
                        1 -> JobLogResponse(JobRecord(id = 1, status = "running"), listOf(JobLogLine(1, "", "a")))
                        2 -> JobLogResponse(JobRecord(id = 1, status = "succeeded"), listOf(JobLogLine(2, "", "b")))
                        else -> JobLogResponse(JobRecord(id = 1, status = "succeeded"), emptyList())
                    },
                )
            }
            override suspend fun cancel(owner: JobOwner, jobId: Long) = Outcome.Success(Unit)
            override suspend fun retry(owner: JobOwner, jobId: Long) = Outcome.Success(Unit)
        }
        val steps = WatchJobUseCase(repo, poll = 1.milliseconds)(JobOwner.Hub, 1).toList()
        assertEquals(listOf("a", "b"), steps.flatMap { it.newLines }.map { it.text })
        assertEquals(3, steps.size)
    }

    @Test
    fun sectionFromPath() {
        assertEquals(HostSectionKey.FINDINGS, HostSectionKey.fromPath("/findings?focus=a,b"))
        assertEquals(HostSectionKey.FAIL2BAN, HostSectionKey.fromPath("/fail2ban?focus=1.2.3.4"))
        assertEquals(HostSectionKey.MALWARE, HostSectionKey.fromPath("/vulnerabilities?tab=malware&focus=x"))
        assertEquals(HostSectionKey.CONTAINERS, HostSectionKey.fromPath("/docker"))
        assertEquals(HostSectionKey.OVERVIEW, HostSectionKey.fromPath("/something-new"))
    }

    private class FakeTerminal(val tmux: Boolean) : com.netknownsthat.domain.repository.TerminalRepository {
        val opened = mutableListOf<TerminalMode>()
        override fun open(host: HostTarget, mode: TerminalMode): com.netknownsthat.domain.repository.TerminalChannel {
            opened += mode
            return object : com.netknownsthat.domain.repository.TerminalChannel {
                override val events = kotlinx.coroutines.flow.emptyFlow<com.netknownsthat.domain.repository.StreamEvent>()
                override suspend fun send(bytes: ByteArray) = Unit
                override suspend fun sendText(text: String) = Unit
                override suspend fun resize(columns: Int, rows: Int) = Unit
                override fun close() = Unit
            }
        }
        override fun openConsole(host: HostTarget, target: com.netknownsthat.domain.repository.ConsoleTarget) = open(host, TerminalMode.SHELL)
        override suspend fun tmuxStatus(host: HostTarget) = Outcome.Success(com.netknownsthat.domain.repository.TmuxStatus(tmux))
        override suspend fun tmuxMouse(host: HostTarget): Outcome<Boolean?> = Outcome.Success(null)
        override suspend fun setTmuxMouse(host: HostTarget, on: Boolean) = Outcome.Success(on)
        override fun installTmux(host: HostTarget) = throw UnsupportedOperationException()
        override suspend fun tmuxInstallStatus(host: HostTarget) = throw UnsupportedOperationException()
    }

    @Test
    fun terminalFallsBackToShellWithoutTmux() = runTest {
        val without = FakeTerminal(tmux = false)
        val r = OpenTerminalUseCase(without)(host, btop = false)
        assertEquals(TerminalMode.SHELL, r.mode)
        assertTrue(r.tmuxMissing)
        val with = FakeTerminal(tmux = true)
        assertEquals(TerminalMode.TMUX, OpenTerminalUseCase(with)(host, btop = false).mode)
        assertEquals(TerminalMode.BTOP, OpenTerminalUseCase(with)(host, btop = true).mode)
    }

    @Test
    fun notificationsSkipHistoryAndFollowHubChoice() = runTest {
        val settings = object : com.netknownsthat.domain.repository.SettingsRepository {
            var last = 0L
            override val language = kotlinx.coroutines.flow.MutableStateFlow<com.netknownsthat.domain.repository.AppLanguage?>(null)
            override suspend fun setLanguage(language: com.netknownsthat.domain.repository.AppLanguage) = Unit
            override val uiScale = kotlinx.coroutines.flow.MutableStateFlow(com.netknownsthat.domain.repository.UiScale.DEFAULT)
            override suspend fun setUiScale(scale: com.netknownsthat.domain.repository.UiScale) = Unit
            override suspend fun betaNoticeHidden() = true
            override suspend fun hideBetaNotice() = Unit
            override val eventNotifications = kotlinx.coroutines.flow.MutableStateFlow(true)
            override suspend fun setEventNotifications(enabled: Boolean) = Unit
            override suspend fun lastNotifiedEventId() = last
            override suspend fun setLastNotifiedEventId(id: Long) { last = id }
        }
        val session = object : com.netknownsthat.domain.repository.SessionRepository {
            override val hubUrl = kotlinx.coroutines.flow.MutableStateFlow<String?>("http://hub")
            override val sessionExpired = kotlinx.coroutines.flow.emptyFlow<Unit>()
            override suspend fun restore() = true
            override suspend fun setHubUrl(raw: String) = Outcome.Success(Unit)
            override suspend fun login(username: String, password: String) = throw UnsupportedOperationException()
            override suspend fun me() = throw UnsupportedOperationException()
            override suspend fun logout() = Unit
            override fun pinnedCertificate(): String? = null
            override fun forgetPinnedCertificate() = Unit
            override suspend fun webSession(): com.netknownsthat.domain.repository.WebSession? = null
        }
        val events = object : EventsRepository {
            var list = listOf(HubEvent(id = 5, kind = "unreachable"), HubEvent(id = 6, kind = "recovered"))
            override suspend fun events(limit: Int, kind: String?, after: Long) =
                Outcome.Success(HubEventsResponse(events = list.filter { it.id > after }, notify = mapOf("unreachable" to true)))
            override suspend fun markSeen() = Outcome.Success(Unit)
        }
        val check = CheckNewEventsUseCase(session, settings, events)
        assertTrue("first run replays nothing", check().isEmpty())
        assertEquals(6L, settings.last)
        events.list = events.list + HubEvent(id = 7, kind = "unreachable") + HubEvent(id = 8, kind = "recovered")
        assertEquals(listOf(7L), check().map { it.id })
    }

    @Test
    fun uiScaleFallsBackToTheDefault() {
        assertEquals(UiScale(80), UiScale.of(80))
        assertEquals(UiScale(115), UiScale.of(115))
        // Any percent 50–100 typed in is kept.
        assertEquals(UiScale(50), UiScale.of(50))
        assertEquals(UiScale(73), UiScale.of(73))
        // Nothing saved yet, or out of range: the default.
        assertEquals(UiScale.DEFAULT, UiScale.of(null))
        assertEquals(UiScale.DEFAULT, UiScale.of(49))
        assertEquals(UiScale.DEFAULT, UiScale.of(105))
    }
}
