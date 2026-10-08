package com.netknownsthat.ui.navigation

import kotlinx.serialization.Serializable

/*
 * Type-safe routes. Every screen and every detail page has its own place in
 * the back stack, so system Back steps back one level and the breadcrumbs
 * (Breadcrumbs.kt) are simply the stack read from the bottom.
 *
 * hostId uses the hub's ids: -1 is the hub's own machine ("localhost").
 * hostName rides along so a crumb can be drawn without a request.
 */

@Serializable data object LoginRoute
@Serializable data object HostsRoute
@Serializable data object EventsRoute
@Serializable data object MonitoringRoute
@Serializable data object HubJobsRoute
@Serializable data object MoreRoute
@Serializable data object FleetFail2banRoute
@Serializable data object DeploymentsRoute
@Serializable data object AboutRoute

/** A host: its sections. */
@Serializable data class HostRoute(val hostId: Long, val hostName: String)

/** One section of a host (HostSectionKey name). */
@Serializable data class HostSectionRoute(val hostId: Long, val hostName: String, val section: String)

/** A Docker container's page: state, actions, inspect. */
@Serializable data class ContainerRoute(val hostId: Long, val hostName: String, val container: String)

/** A console: kind is ConsoleKind's name, user — Docker/Podman only. */
@Serializable data class ConsoleRoute(val hostId: Long, val hostName: String, val kind: String, val name: String, val user: String = "")

/** A machine's screen: kind "vm" or "lxd", proto "vnc" or "spice". */
@Serializable data class VmScreenRoute(val hostId: Long, val hostName: String, val kind: String, val name: String, val proto: String)

/** A monitoring target's history charts. */
@Serializable data class TargetHistoryRoute(val hostId: Long, val hostName: String, val targetId: Long, val label: String)

/** A host's charts in the hub's monitoring. */
@Serializable data class MonHostRoute(val hostId: Long, val hostName: String, val memTotal: Double = 0.0)

/** A configuration file: view, edit, history. */
@Serializable data class ConfigFileRoute(val hostId: Long, val hostName: String, val path: String)

/** A job's live log. hostId == HUB_JOBS — a hub job. */
@Serializable data class JobRoute(val hostId: Long, val hostName: String, val jobId: Long)

/** A pipeline: history, rollback. */
@Serializable data class PipelineRoute(val pipelineId: Long, val name: String)

/** hostId of hub jobs in [JobRoute] — no real host has id 0. */
const val HUB_JOBS = 0L
