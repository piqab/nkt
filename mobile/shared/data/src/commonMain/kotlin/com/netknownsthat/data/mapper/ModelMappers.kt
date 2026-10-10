package com.netknownsthat.data.mapper

import com.netknownsthat.domain.model.ActiveJob
import com.netknownsthat.domain.model.OsInfo
import com.netknownsthat.data.dto.ActiveJobDto
import com.netknownsthat.data.dto.OsInfoDto
import com.netknownsthat.data.dto.AddHostResponseDto
import com.netknownsthat.data.dto.AddedRuleDto
import com.netknownsthat.data.dto.AuditEntryDto
import com.netknownsthat.data.dto.AuditResponseDto
import com.netknownsthat.data.dto.AvailabilitySummaryDto
import com.netknownsthat.data.dto.CertRenewalDto
import com.netknownsthat.data.dto.CertSummaryDto
import com.netknownsthat.data.dto.CertificateDto
import com.netknownsthat.data.dto.CertificatesResponseDto
import com.netknownsthat.data.dto.CertificatesSummaryDto
import com.netknownsthat.data.dto.ClamHitDto
import com.netknownsthat.data.dto.ClamResponseDto
import com.netknownsthat.data.dto.ClamScanDto
import com.netknownsthat.data.dto.ClamStatusDto
import com.netknownsthat.data.dto.CommandResultDto
import com.netknownsthat.data.dto.CommandStatusDto
import com.netknownsthat.data.dto.ConfigDiffResponseDto
import com.netknownsthat.data.dto.ConfigFileResponseDto
import com.netknownsthat.data.dto.ConfigVersionDto
import com.netknownsthat.data.dto.ConfigVersionsResponseDto
import com.netknownsthat.data.dto.ConfigWriteResultDto
import com.netknownsthat.data.dto.ConfigsResponseDto
import com.netknownsthat.data.dto.ContainerDto
import com.netknownsthat.data.dto.ContainerInspectDto
import com.netknownsthat.data.dto.ContainerNetworkDto
import com.netknownsthat.data.dto.ContainerPortDto
import com.netknownsthat.data.dto.ContainersResponseDto
import com.netknownsthat.data.dto.DeploymentDto
import com.netknownsthat.data.dto.DeploymentsResponseDto
import com.netknownsthat.data.dto.DockerImageDto
import com.netknownsthat.data.dto.DockerNetworkDto
import com.netknownsthat.data.dto.Fail2banBanDto
import com.netknownsthat.data.dto.Fail2banJailDto
import com.netknownsthat.data.dto.Fail2banResponseDto
import com.netknownsthat.data.dto.Fail2banStateDto
import com.netknownsthat.data.dto.FindingDto
import com.netknownsthat.data.dto.FindingsResponseDto
import com.netknownsthat.data.dto.FirewallManagerDto
import com.netknownsthat.data.dto.FirewallNumberedResponseDto
import com.netknownsthat.data.dto.FirewallPolicyDto
import com.netknownsthat.data.dto.FirewallResponseDto
import com.netknownsthat.data.dto.FirewallRuleDto
import com.netknownsthat.data.dto.FirewalldPortSpecDto
import com.netknownsthat.data.dto.FleetBannedIPDto
import com.netknownsthat.data.dto.FleetBannedOnDto
import com.netknownsthat.data.dto.FleetBannedResponseDto
import com.netknownsthat.data.dto.FleetF2BHostDto
import com.netknownsthat.data.dto.HAProxyPathsResponseDto
import com.netknownsthat.data.dto.HostGroupsResponseDto
import com.netknownsthat.data.dto.HostInfoDto
import com.netknownsthat.data.dto.HubEventDto
import com.netknownsthat.data.dto.HubEventsResponseDto
import com.netknownsthat.data.dto.HubHostDto
import com.netknownsthat.data.dto.HubVersionInfoDto
import com.netknownsthat.data.dto.HubVulnDBInfoDto
import com.netknownsthat.data.dto.ImageActionResponseDto
import com.netknownsthat.data.dto.ImageOutcomeDto
import com.netknownsthat.data.dto.ImagesResponseDto
import com.netknownsthat.data.dto.InspectEnvDto
import com.netknownsthat.data.dto.InspectMountDto
import com.netknownsthat.data.dto.InspectNetDto
import com.netknownsthat.data.dto.InspectPortDto
import com.netknownsthat.data.dto.InstallJobResponseDto
import com.netknownsthat.data.dto.InstalledResponseDto
import com.netknownsthat.data.dto.InterfacesResponseDto
import com.netknownsthat.data.dto.JobDto
import com.netknownsthat.data.dto.JobIdResponseDto
import com.netknownsthat.data.dto.JobListResponseDto
import com.netknownsthat.data.dto.JobLogLineDto
import com.netknownsthat.data.dto.JobLogResponseDto
import com.netknownsthat.data.dto.JobRecordDto
import com.netknownsthat.data.dto.JobStartedDto
import com.netknownsthat.data.dto.JobsResponseDto
import com.netknownsthat.data.dto.K8sUsageClusterDto
import com.netknownsthat.data.dto.K8sUsageNodeDto
import com.netknownsthat.data.dto.LXDInstanceDto
import com.netknownsthat.data.dto.LXDResponseDto
import com.netknownsthat.data.dto.LeftoverStackDto
import com.netknownsthat.data.dto.LineageInfoDto
import com.netknownsthat.data.dto.LineagesResponseDto
import com.netknownsthat.data.dto.ListenerDto
import com.netknownsthat.data.dto.LogSourceDto
import com.netknownsthat.data.dto.LogSourcesResponseDto
import com.netknownsthat.data.dto.LogTailResponseDto
import com.netknownsthat.data.dto.ManagedFileDto
import com.netknownsthat.data.dto.MeDto
import com.netknownsthat.data.dto.MiscResponseDto
import com.netknownsthat.data.dto.MonClusterDto
import com.netknownsthat.data.dto.MonClusterNodeDto
import com.netknownsthat.data.dto.MonDiskDto
import com.netknownsthat.data.dto.MonHostDto
import com.netknownsthat.data.dto.MonInsightDto
import com.netknownsthat.data.dto.MonitoringOverviewDto
import com.netknownsthat.data.dto.NetworkInterfaceDto
import com.netknownsthat.data.dto.NumberedRuleDto
import com.netknownsthat.data.dto.OutageDto
import com.netknownsthat.data.dto.OutagesResponseDto
import com.netknownsthat.data.dto.OverviewDto
import com.netknownsthat.data.dto.PipelineDto
import com.netknownsthat.data.dto.PipelinesResponseDto
import com.netknownsthat.data.dto.PodmanContainerDto
import com.netknownsthat.data.dto.PodmanResponseDto
import com.netknownsthat.data.dto.RebootNoAutoDto
import com.netknownsthat.data.dto.RebootPreviewDto
import com.netknownsthat.data.dto.RenewEventDto
import com.netknownsthat.data.dto.RenewJobStatusDto
import com.netknownsthat.data.dto.RuleSpecDto
import com.netknownsthat.data.dto.SelfSignedResponseDto
import com.netknownsthat.data.dto.SelfSignedResultDto
import com.netknownsthat.data.dto.ServiceUnitDto
import com.netknownsthat.data.dto.ServicesResponseDto
import com.netknownsthat.data.dto.TargetDto
import com.netknownsthat.data.dto.TargetsResponseDto
import com.netknownsthat.data.dto.TopologyEdgeDto
import com.netknownsthat.data.dto.TopologyFindingDto
import com.netknownsthat.data.dto.TopologyNodeDto
import com.netknownsthat.data.dto.TopologyResponseDto
import com.netknownsthat.data.dto.UsagePointDto
import com.netknownsthat.data.dto.UsageResponseDto
import com.netknownsthat.data.dto.UsageSourcesResponseDto
import com.netknownsthat.data.dto.UsageTopEntryDto
import com.netknownsthat.data.dto.UsageTopResponseDto
import com.netknownsthat.data.dto.UserDto
import com.netknownsthat.data.dto.UsersResponseDto
import com.netknownsthat.data.dto.VMsResponseDto
import com.netknownsthat.data.dto.VirtualMachineDto
import com.netknownsthat.data.dto.VulnFindingDto
import com.netknownsthat.data.dto.VulnResponseDto
import com.netknownsthat.data.dto.VulnScanDto
import com.netknownsthat.domain.model.AddHostResponse
import com.netknownsthat.domain.model.AddedRule
import com.netknownsthat.domain.model.AuditEntry
import com.netknownsthat.domain.model.AuditResponse
import com.netknownsthat.domain.model.AvailabilitySummary
import com.netknownsthat.domain.model.CertRenewal
import com.netknownsthat.domain.model.CertSummary
import com.netknownsthat.domain.model.Certificate
import com.netknownsthat.domain.model.CertificatesResponse
import com.netknownsthat.domain.model.CertificatesSummary
import com.netknownsthat.domain.model.ClamHit
import com.netknownsthat.domain.model.ClamResponse
import com.netknownsthat.domain.model.ClamScan
import com.netknownsthat.domain.model.ClamStatus
import com.netknownsthat.domain.model.CommandResult
import com.netknownsthat.domain.model.CommandStatus
import com.netknownsthat.domain.model.ConfigDiffResponse
import com.netknownsthat.domain.model.ConfigFileResponse
import com.netknownsthat.domain.model.ConfigVersion
import com.netknownsthat.domain.model.ConfigVersionsResponse
import com.netknownsthat.domain.model.ConfigWriteResult
import com.netknownsthat.domain.model.ConfigsResponse
import com.netknownsthat.domain.model.Container
import com.netknownsthat.domain.model.ContainerInspect
import com.netknownsthat.domain.model.ContainerNetwork
import com.netknownsthat.domain.model.ContainerPort
import com.netknownsthat.domain.model.ContainersResponse
import com.netknownsthat.domain.model.Deployment
import com.netknownsthat.domain.model.DeploymentsResponse
import com.netknownsthat.domain.model.DockerImage
import com.netknownsthat.domain.model.DockerNetwork
import com.netknownsthat.domain.model.Fail2banBan
import com.netknownsthat.domain.model.Fail2banJail
import com.netknownsthat.domain.model.Fail2banResponse
import com.netknownsthat.domain.model.Fail2banState
import com.netknownsthat.domain.model.Finding
import com.netknownsthat.domain.model.FindingsResponse
import com.netknownsthat.domain.model.FirewallManager
import com.netknownsthat.domain.model.FirewallNumberedResponse
import com.netknownsthat.domain.model.FirewallPolicy
import com.netknownsthat.domain.model.FirewallResponse
import com.netknownsthat.domain.model.FirewallRule
import com.netknownsthat.domain.model.FirewalldPortSpec
import com.netknownsthat.domain.model.FleetBannedIP
import com.netknownsthat.domain.model.FleetBannedOn
import com.netknownsthat.domain.model.FleetBannedResponse
import com.netknownsthat.domain.model.FleetF2BHost
import com.netknownsthat.domain.model.HAProxyPathsResponse
import com.netknownsthat.domain.model.HostGroupsResponse
import com.netknownsthat.domain.model.HostInfo
import com.netknownsthat.domain.model.HubEvent
import com.netknownsthat.domain.model.HubEventsResponse
import com.netknownsthat.domain.model.HubHost
import com.netknownsthat.domain.model.HubVersionInfo
import com.netknownsthat.domain.model.HubVulnDBInfo
import com.netknownsthat.domain.model.ImageActionResponse
import com.netknownsthat.domain.model.ImageOutcome
import com.netknownsthat.domain.model.ImagesResponse
import com.netknownsthat.domain.model.InspectEnv
import com.netknownsthat.domain.model.InspectMount
import com.netknownsthat.domain.model.InspectNet
import com.netknownsthat.domain.model.InspectPort
import com.netknownsthat.domain.model.InstallJobResponse
import com.netknownsthat.domain.model.InstalledResponse
import com.netknownsthat.domain.model.InterfacesResponse
import com.netknownsthat.domain.model.Job
import com.netknownsthat.domain.model.JobIdResponse
import com.netknownsthat.domain.model.JobListResponse
import com.netknownsthat.domain.model.JobLogLine
import com.netknownsthat.domain.model.JobLogResponse
import com.netknownsthat.domain.model.JobRecord
import com.netknownsthat.domain.model.JobStarted
import com.netknownsthat.domain.model.JobsResponse
import com.netknownsthat.domain.model.K8sUsageCluster
import com.netknownsthat.domain.model.K8sUsageNode
import com.netknownsthat.domain.model.LXDInstance
import com.netknownsthat.domain.model.LXDResponse
import com.netknownsthat.domain.model.LeftoverStack
import com.netknownsthat.domain.model.LineageInfo
import com.netknownsthat.domain.model.LineagesResponse
import com.netknownsthat.domain.model.Listener
import com.netknownsthat.domain.model.LogSource
import com.netknownsthat.domain.model.LogSourcesResponse
import com.netknownsthat.domain.model.LogTailResponse
import com.netknownsthat.domain.model.ManagedFile
import com.netknownsthat.domain.model.Me
import com.netknownsthat.domain.model.MiscResponse
import com.netknownsthat.domain.model.MonCluster
import com.netknownsthat.domain.model.MonClusterNode
import com.netknownsthat.domain.model.MonDisk
import com.netknownsthat.domain.model.MonHost
import com.netknownsthat.domain.model.MonInsight
import com.netknownsthat.domain.model.MonitoringOverview
import com.netknownsthat.domain.model.NetworkInterface
import com.netknownsthat.domain.model.NumberedRule
import com.netknownsthat.domain.model.Outage
import com.netknownsthat.domain.model.OutagesResponse
import com.netknownsthat.domain.model.Overview
import com.netknownsthat.domain.model.Pipeline
import com.netknownsthat.domain.model.PipelinesResponse
import com.netknownsthat.domain.model.PodmanContainer
import com.netknownsthat.domain.model.PodmanResponse
import com.netknownsthat.domain.model.RebootNoAuto
import com.netknownsthat.domain.model.RebootPreview
import com.netknownsthat.domain.model.RenewEvent
import com.netknownsthat.domain.model.RenewJobStatus
import com.netknownsthat.domain.model.RuleSpec
import com.netknownsthat.domain.model.SelfSignedResponse
import com.netknownsthat.domain.model.SelfSignedResult
import com.netknownsthat.domain.model.ServiceUnit
import com.netknownsthat.domain.model.ServicesResponse
import com.netknownsthat.domain.model.Target
import com.netknownsthat.domain.model.TargetsResponse
import com.netknownsthat.domain.model.TopologyEdge
import com.netknownsthat.domain.model.TopologyFinding
import com.netknownsthat.domain.model.TopologyNode
import com.netknownsthat.domain.model.TopologyResponse
import com.netknownsthat.domain.model.UsagePoint
import com.netknownsthat.domain.model.UsageResponse
import com.netknownsthat.domain.model.UsageSourcesResponse
import com.netknownsthat.domain.model.UsageTopEntry
import com.netknownsthat.domain.model.UsageTopResponse
import com.netknownsthat.domain.model.User
import com.netknownsthat.domain.model.UsersResponse
import com.netknownsthat.domain.model.VMsResponse
import com.netknownsthat.domain.model.VirtualMachine
import com.netknownsthat.domain.model.VulnFinding
import com.netknownsthat.domain.model.VulnResponse
import com.netknownsthat.domain.model.VulnScan

// DTO → domain mappers. Generated once from the former android/ models when
// the app moved to Kotlin Multiplatform (Clean Architecture: the domain never
// sees serialization annotations); maintained by hand from here on.

fun ConfigsResponseDto.toDomain(): ConfigsResponse = ConfigsResponse(
    files = files.map { it.toDomain() },
)

fun ManagedFileDto.toDomain(): ManagedFile = ManagedFile(
    path = path,
    service = service,
    size = size,
    modTime = modTime,
    sha256 = sha256,
    editable = editable,
    readable = readable,
)

fun ConfigFileResponseDto.toDomain(): ConfigFileResponse = ConfigFileResponse(
    path = path,
    content = content,
    sha256 = sha256,
    editable = editable,
)

fun ConfigVersionsResponseDto.toDomain(): ConfigVersionsResponse = ConfigVersionsResponse(
    versions = versions.map { it.toDomain() },
)

fun ConfigVersionDto.toDomain(): ConfigVersion = ConfigVersion(
    id = id,
    path = path,
    service = service,
    ts = ts,
    author = author,
    action = action,
    note = note,
    size = size,
    sha256 = sha256,
)

fun ConfigDiffResponseDto.toDomain(): ConfigDiffResponse = ConfigDiffResponse(
    diff = diff,
)

fun ConfigWriteResultDto.toDomain(): ConfigWriteResult = ConfigWriteResult(
    path = path,
    versionId = versionId,
    validated = validated,
    validation = validation?.toDomain(),
    rolledBack = rolledBack,
    message = message,
    applied = applied,
    apply = apply?.toDomain(),
)

fun CommandResultDto.toDomain(): CommandResult = CommandResult(
    argv = argv,
    exitCode = exitCode,
    stdout = stdout,
    stderr = stderr,
    simulated = simulated,
)

fun FirewallResponseDto.toDomain(): FirewallResponse = FirewallResponse(
    managers = managers.map { it.toDomain() },
    backends = backends,
    policies = policies.map { it.toDomain() },
    rules = rules.map { it.toDomain() },
    listeners = listeners.map { it.toDomain() },
)

fun FirewallManagerDto.toDomain(): FirewallManager = FirewallManager(
    name = name,
    installed = installed,
    active = active,
    policy = policy,
)

fun FirewallPolicyDto.toDomain(): FirewallPolicy = FirewallPolicy(
    backend = backend,
    table = table,
    chain = chain,
    policy = policy,
    packets = packets,
    bytes = bytes,
)

fun FirewallRuleDto.toDomain(): FirewallRule = FirewallRule(
    id = id,
    backend = backend,
    table = table,
    chain = chain,
    order = order,
    action = action,
    inIface = inIface,
    packets = packets,
    bytes = bytes,
    raw = raw,
    managedBy = managedBy,
)

fun FirewallNumberedResponseDto.toDomain(): FirewallNumberedResponse = FirewallNumberedResponse(
    rules = rules.map { it.toDomain() },
    added = added.map { it.toDomain() },
)

fun NumberedRuleDto.toDomain(): NumberedRule = NumberedRule(
    number = number,
    text = text,
)

fun AddedRuleDto.toDomain(): AddedRule = AddedRule(
    spec = spec,
    action = action,
    port = port,
    protocol = protocol,
)

fun RuleSpecDto.toDomain(): RuleSpec = RuleSpec(
    action = action,
    port = port,
    protocol = protocol,
    from = from,
    comment = comment,
)

fun FirewalldPortSpecDto.toDomain(): FirewalldPortSpec = FirewalldPortSpec(
    zone = zone,
    port = port,
    protocol = protocol,
    service = service,
    permanent = permanent,
    runtime = runtime,
)

fun CommandStatusDto.toDomain(): CommandStatus = CommandStatus(
    status = status,
    output = output,
    simulated = simulated,
)

fun LineageInfoDto.toDomain(): LineageInfo = LineageInfo(
    name = name,
    nameUnicode = nameUnicode,
    known = known,
    notAfter = notAfter,
    daysLeft = daysLeft,
)

fun LineagesResponseDto.toDomain(): LineagesResponse = LineagesResponse(
    lineages = lineages.map { it.toDomain() },
)

fun HAProxyPathsResponseDto.toDomain(): HAProxyPathsResponse = HAProxyPathsResponse(
    paths = paths,
)

fun JobStartedDto.toDomain(): JobStarted = JobStarted(
    job = job,
)

fun RenewJobStatusDto.toDomain(): RenewJobStatus = RenewJobStatus(
    events = events.map { it.toDomain() },
    done = done,
    error = error,
)

fun RenewEventDto.toDomain(): RenewEvent = RenewEvent(
    time = time,
    text = text,
)

fun SelfSignedResponseDto.toDomain(): SelfSignedResponse = SelfSignedResponse(
    results = results.map { it.toDomain() },
    names = names,
    certPath = certPath,
    snippet = snippet,
    notAfter = notAfter,
)

fun SelfSignedResultDto.toDomain(): SelfSignedResult = SelfSignedResult(
    names = names,
    certPath = certPath,
    keyPath = keyPath,
    combinedPath = combinedPath,
    fingerprint = fingerprint,
    notAfter = notAfter,
    snippet = snippet,
)

fun CertificatesResponseDto.toDomain(): CertificatesResponse = CertificatesResponse(
    certificates = certificates.map { it.toDomain() },
    summary = summary?.toDomain(),
)

fun CertificatesSummaryDto.toDomain(): CertificatesSummary = CertificatesSummary(
    total = total,
    expired = expired,
    expiring = expiring,
    unreadable = unreadable,
    unmanaged = unmanaged,
)

fun CertificateDto.toDomain(): Certificate = Certificate(
    id = id,
    path = path,
    service = service,
    names = names,
    subject = subject,
    issuer = issuer,
    notBefore = notBefore,
    notAfter = notAfter,
    daysLeft = daysLeft,
    keyAlgorithm = keyAlgorithm,
    keyBits = keyBits,
    selfSigned = selfSigned,
    fingerprint = fingerprint,
    renewal = renewal?.toDomain(),
    error = error,
)

fun CertRenewalDto.toDomain(): CertRenewal = CertRenewal(
    tool = tool,
    managed = managed,
    automatic = automatic,
    detail = detail,
    lineage = lineage,
)

fun TopologyResponseDto.toDomain(): TopologyResponse = TopologyResponse(
    nodes = nodes.map { it.toDomain() },
    edges = edges.map { it.toDomain() },
    stats = stats,
    findings = findings.map { it.toDomain() },
)

fun TopologyNodeDto.toDomain(): TopologyNode = TopologyNode(
    id = id,
    kind = kind,
    label = label,
    status = status,
    findings = findings,
    group = group,
    meta = meta,
)

fun TopologyEdgeDto.toDomain(): TopologyEdge = TopologyEdge(
    id = id,
    from = from,
    to = to,
    kind = kind,
    label = label,
    status = status,
)

fun TopologyFindingDto.toDomain(): TopologyFinding = TopologyFinding(
    nodeId = nodeId,
    title = title,
    severity = severity,
)

fun LogSourcesResponseDto.toDomain(): LogSourcesResponse = LogSourcesResponse(
    sources = sources.map { it.toDomain() },
    root = root,
)

fun LogSourceDto.toDomain(): LogSource = LogSource(
    kind = kind,
    name = name,
    size = size,
    service = service,
    archived = archived,
    compressed = compressed,
)

fun LogTailResponseDto.toDomain(): LogTailResponse = LogTailResponse(
    output = output,
)

fun OverviewDto.toDomain(): Overview = Overview(
    host = host.toDomain(),
    mode = mode,
    scanned = scanned,
    scanMs = scanMs,
    simulated = simulated,
    version = version,
    counts = counts,
    findings = findings,
    certificates = certificates?.toDomain(),
    availability = availability?.toDomain(),
)

fun HostInfoDto.toDomain(): HostInfo = HostInfo(
    mode = mode,
    hostname = hostname,
    kernel = kernel,
    os = os,
    notes = notes,
)

fun CertSummaryDto.toDomain(): CertSummary = CertSummary(
    total = total,
    expired = expired,
    expiring = expiring,
    unreadable = unreadable,
    unmanaged = unmanaged,
    soonestDays = soonestDays,
    soonestName = soonestName,
)

fun AvailabilitySummaryDto.toDomain(): AvailabilitySummary = AvailabilitySummary(
    targets = targets,
    up = up,
    down = down,
    avgUptime = avgUptime,
)

fun FindingsResponseDto.toDomain(): FindingsResponse = FindingsResponse(
    findings = findings.map { it.toDomain() },
    counts = counts,
    total = total,
)

fun FindingDto.toDomain(): Finding = Finding(
    id = id,
    rule = rule,
    severity = severity,
    title = title,
    detail = detail,
    service = service,
    `object` = `object`,
    file = file,
    line = line,
    suggestion = suggestion,
    refs = refs,
)

fun InterfacesResponseDto.toDomain(): InterfacesResponse = InterfacesResponse(
    interfaces = interfaces.map { it.toDomain() },
)

fun NetworkInterfaceDto.toDomain(): NetworkInterface = NetworkInterface(
    name = name,
    mac = mac,
    mtu = mtu,
    up = up,
    lowerUp = lowerUp,
    loopback = loopback,
    addresses = addresses,
    rxBytes = rxBytes,
    txBytes = txBytes,
    rxErrors = rxErrors,
    rxDropped = rxDropped,
    txErrors = txErrors,
    txDropped = txDropped,
    dockerNetwork = dockerNetwork,
    attachedContainers = attachedContainers,
)

fun AuditResponseDto.toDomain(): AuditResponse = AuditResponse(
    entries = entries.map { it.toDomain() },
)

fun AuditEntryDto.toDomain(): AuditEntry = AuditEntry(
    id = id,
    ts = ts,
    username = username,
    action = action,
    target = target,
    result = result,
    detail = detail,
)

fun HubEventsResponseDto.toDomain(): HubEventsResponse = HubEventsResponse(
    events = events.map { it.toDomain() },
    unread = unread,
    total = total,
    hosts = hosts,
    kinds = kinds,
    notify = notify,
)

fun HubEventDto.toDomain(): HubEvent = HubEvent(
    id = id,
    ts = ts,
    hostId = hostId,
    hostName = hostName,
    hostAddr = hostAddr,
    kind = kind,
    severity = severity,
    detail = detail,
    link = link,
)

fun JobListResponseDto.toDomain(): JobListResponse = JobListResponse(
    jobs = jobs.map { it.toDomain() },
    total = total,
    active = active,
    kinds = kinds,
)

fun JobRecordDto.toDomain(): JobRecord = JobRecord(
    id = id,
    kind = kind,
    title = title,
    queue = queue,
    status = status,
    step = step,
    steps = steps,
    stepName = stepName,
    error = error,
    author = author,
    createdAt = createdAt,
    startedAt = startedAt,
    finishedAt = finishedAt,
)

fun ActiveJobDto.toDomain(): ActiveJob = ActiveJob(hostId = hostId, hostName = hostName, job = job.toDomain())

fun JobLogResponseDto.toDomain(): JobLogResponse = JobLogResponse(
    job = job.toDomain(),
    lines = lines.map { it.toDomain() },
)

fun JobLogLineDto.toDomain(): JobLogLine = JobLogLine(
    seq = seq,
    ts = ts,
    text = text,
)

fun JobIdResponseDto.toDomain(): JobIdResponse = JobIdResponse(
    jobId = jobId,
    deploymentId = deploymentId,
)

fun Fail2banResponseDto.toDomain(): Fail2banResponse = Fail2banResponse(
    state = state.toDomain(),
    manualJail = manualJail,
    manualReady = manualReady,
    hubAddr = hubAddr,
    clientIp = clientIp,
    simulated = simulated,
)

fun Fail2banStateDto.toDomain(): Fail2banState = Fail2banState(
    installed = installed,
    running = running,
    version = version,
    jails = jails.map { it.toDomain() },
)

fun Fail2banJailDto.toDomain(): Fail2banJail = Fail2banJail(
    name = name,
    maxRetry = maxRetry,
    findTime = findTime,
    banTime = banTime,
    failed = failed,
    totalFailed = totalFailed,
    banned = banned,
    totalBanned = totalBanned,
    bans = bans.map { it.toDomain() },
)

fun Fail2banBanDto.toDomain(): Fail2banBan = Fail2banBan(
    ip = ip,
    jail = jail,
    since = since,
    until = until,
)

fun FleetBannedResponseDto.toDomain(): FleetBannedResponse = FleetBannedResponse(
    hosts = hosts.map { it.toDomain() },
    ips = ips.map { it.toDomain() },
)

fun FleetF2BHostDto.toDomain(): FleetF2BHost = FleetF2BHost(
    id = id,
    name = name,
    installed = installed,
    running = running,
    banned = banned,
    error = error,
)

fun FleetBannedIPDto.toDomain(): FleetBannedIP = FleetBannedIP(
    ip = ip,
    hosts = hosts.map { it.toDomain() },
)

fun FleetBannedOnDto.toDomain(): FleetBannedOn = FleetBannedOn(
    id = id,
    name = name,
    jails = jails,
)

fun RebootPreviewDto.toDomain(): RebootPreview = RebootPreview(
    rebootRequired = rebootRequired,
    running = running,
    noAutostart = noAutostart.map { it.toDomain() },
    simulated = simulated,
)

fun RebootNoAutoDto.toDomain(): RebootNoAuto = RebootNoAuto(
    kind = kind,
    name = name,
    reason = reason,
)

fun MonitoringOverviewDto.toDomain(): MonitoringOverview = MonitoringOverview(
    hosts = hosts.map { it.toDomain() },
    insights = insights.map { it.toDomain() },
    clusters = clusters.map { it.toDomain() },
    collecting = collecting,
    lastRun = lastRun,
)

fun MonHostDto.toDomain(): MonHost = MonHost(
    id = id,
    name = name,
    group = group,
    reachable = reachable,
    hasData = hasData,
    cpuNow = cpuNow,
    cpuAvg = cpuAvg,
    cpuMax = cpuMax,
    memUsed = memUsed,
    memTotal = memTotal,
    memAvgPct = memAvgPct,
    loadAvg = loadAvg,
    disks = disks.map { it.toDomain() },
    workloads = workloads,
    k8sRole = k8sRole,
    k8sNode = k8sNode,
)

fun MonDiskDto.toDomain(): MonDisk = MonDisk(
    mount = mount,
    used = used,
    size = size,
    pct = pct,
    etaDays = etaDays,
)

fun MonInsightDto.toDomain(): MonInsight = MonInsight(
    kind = kind,
    severity = severity,
    hostId = hostId,
    host = host,
    text = text,
    path = path,
)

fun MonClusterDto.toDomain(): MonCluster = MonCluster(
    name = name,
    hostId = hostId,
    nodes = nodes.map { it.toDomain() },
)

fun MonClusterNodeDto.toDomain(): MonClusterNode = MonClusterNode(
    name = name,
    ip = ip,
    ready = ready,
    controlPlane = controlPlane,
)

fun PipelinesResponseDto.toDomain(): PipelinesResponse = PipelinesResponse(
    pipelines = pipelines.map { it.toDomain() },
)

fun PipelineDto.toDomain(): Pipeline = Pipeline(
    id = id,
    name = name,
    enabled = enabled,
    action = action,
    lastCommit = lastCommit,
    lastTag = lastTag,
    last = last?.toDomain(),
    leftovers = leftovers.map { it.toDomain() },
)

fun DeploymentDto.toDomain(): Deployment = Deployment(
    id = id,
    ref = ref,
    commit = commit,
    tag = tag,
    trigger = trigger,
    author = author,
    jobId = jobId,
    status = status,
    error = error,
    createdAt = createdAt,
    finishedAt = finishedAt,
)

fun DeploymentsResponseDto.toDomain(): DeploymentsResponse = DeploymentsResponse(
    deployments = deployments.map { it.toDomain() },
)

fun LeftoverStackDto.toDomain(): LeftoverStack = LeftoverStack(
    hostId = hostId,
    host = host,
    project = project,
    reason = reason,
    at = at,
)

fun ContainerInspectDto.toDomain(): ContainerInspect = ContainerInspect(
    name = name,
    id = id,
    image = image,
    imageId = imageId,
    imageOutdated = imageOutdated,
    state = state,
    health = health,
    exitCode = exitCode,
    created = created,
    startedAt = startedAt,
    restartCount = restartCount,
    restartPolicy = restartPolicy,
    memoryLimit = memoryLimit,
    nanoCpus = nanoCpus,
    entrypoint = entrypoint,
    cmd = cmd,
    user = user,
    workingDir = workingDir,
    composeProject = composeProject,
    composeService = composeService,
    env = env.map { it.toDomain() },
    revealed = revealed,
    ports = ports.map { it.toDomain() },
    mounts = mounts.map { it.toDomain() },
    networks = networks.map { it.toDomain() },
    labels = labels,
)

fun InspectEnvDto.toDomain(): InspectEnv = InspectEnv(
    name = name,
    value = value,
    masked = masked,
    origin = origin,
)

fun InspectPortDto.toDomain(): InspectPort = InspectPort(
    container = container,
    hostIp = hostIp,
    hostPort = hostPort,
)

fun InspectMountDto.toDomain(): InspectMount = InspectMount(
    type = type,
    source = source,
    name = name,
    destination = destination,
    rw = rw,
)

fun InspectNetDto.toDomain(): InspectNet = InspectNet(
    name = name,
    ip = ip,
    gateway = gateway,
    aliases = aliases,
)

fun InstalledResponseDto.toDomain(): InstalledResponse = InstalledResponse(
    installed = installed,
)

fun ClamResponseDto.toDomain(): ClamResponse = ClamResponse(
    status = status.toDomain(),
    running = running,
    op = op,
    jobId = jobId,
    hostScan = hostScan?.toDomain(),
    imageScan = imageScan?.toDomain(),
    paths = paths,
    images = images,
    apt = apt,
)

fun ClamStatusDto.toDomain(): ClamStatus = ClamStatus(
    installed = installed,
    version = version,
    dbVersion = dbVersion,
    dbDate = dbDate,
    dbPresent = dbPresent,
    freshclamActive = freshclamActive,
)

fun ClamScanDto.toDomain(): ClamScan = ClamScan(
    kind = kind,
    targets = targets,
    finishedAt = finishedAt,
    scanned = scanned,
    hits = hits.map { it.toDomain() },
    error = error,
)

fun ClamHitDto.toDomain(): ClamHit = ClamHit(
    path = path,
    signature = signature,
    target = target,
)

fun HostGroupsResponseDto.toDomain(): HostGroupsResponse = HostGroupsResponse(
    groups = groups,
)

fun AddHostResponseDto.toDomain(): AddHostResponse = AddHostResponse(
    id = id,
    authorizedKey = authorizedKey,
)

fun InstallJobResponseDto.toDomain(): InstallJobResponse = InstallJobResponse(
    job = job,
)

fun MeDto.toDomain(): Me = Me(
    username = username,
    role = role,
    isAdmin = isAdmin,
    mode = mode,
    allowMutations = allowMutations,
    simulated = simulated,
    hubVersion = hubVersion,
)

fun HubHostDto.toDomain(): HubHost = HubHost(
    id = id,
    name = name,
    addr = addr,
    sshPort = sshPort,
    sshUser = sshUser,
    sshAuthKind = sshAuthKind,
    arch = arch,
    status = status,
    nktVersion = nktVersion,
    sudoStatus = sudoStatus,
    terminalEnabled = terminalEnabled,
    tunnelEnabled = tunnelEnabled,
    errorMsg = errorMsg,
    createdAt = createdAt,
    lastSeenAt = lastSeenAt,
    findings = findings,
    reachable = reachable,
    runningVersion = runningVersion,
    lastPolledAt = lastPolledAt,
    channel = channel,
    tunnelConnected = tunnelConnected,
    group = group,
    k8sRole = k8sRole,
    installActive = installActive,
    hubVersion = hubVersion,
    osInfo = osInfo?.toDomain(),
    parentId = parentId,
    vmState = vmState,
    vmGraphics = vmGraphics,
)

fun HubVersionInfoDto.toDomain(): HubVersionInfo = HubVersionInfo(
    current = current,
    latest = latest,
    updateAvailable = updateAvailable,
    updatable = updatable,
    checkedAt = checkedAt,
    checkError = checkError,
    notes = notes,
)

fun HubVulnDBInfoDto.toDomain(): HubVulnDBInfo = HubVulnDBInfo(
    available = available,
    refreshing = refreshing,
    updatedAt = updatedAt,
    progress = progress,
    error = error,
)

fun TargetsResponseDto.toDomain(): TargetsResponse = TargetsResponse(
    targets = targets.map { it.toDomain() },
    interval = interval,
    simulated = simulated,
)

fun TargetDto.toDomain(): Target = Target(
    id = id,
    label = label,
    kind = kind,
    host = host,
    port = port,
    path = path,
    source = source,
    service = service,
    enabled = enabled,
    lastCheck = lastCheck,
    lastOk = lastOk,
    lastLatencyMs = lastLatencyMs,
    lastError = lastError,
    checks24h = checks24h,
    failures24h = failures24h,
    uptime24h = uptime24h,
    avgLatency24h = avgLatency24h,
)

fun OutagesResponseDto.toDomain(): OutagesResponse = OutagesResponse(
    outages = outages.map { it.toDomain() },
)

fun OutageDto.toDomain(): Outage = Outage(
    targetId = targetId,
    label = label,
    start = start,
    end = end,
    checks = checks,
    error = error,
)

fun UsageResponseDto.toDomain(): UsageResponse = UsageResponse(
    metric = metric,
    source = source,
    total = total,
    simulated = simulated,
    points = points.map { it.toDomain() },
)

fun UsagePointDto.toDomain(): UsagePoint = UsagePoint(
    bucket = bucket,
    subject = subject,
    value = value,
)

fun UsageTopResponseDto.toDomain(): UsageTopResponse = UsageTopResponse(
    metric = metric,
    source = source,
    top = top.map { it.toDomain() },
)

fun UsageTopEntryDto.toDomain(): UsageTopEntry = UsageTopEntry(
    subject = subject,
    total = total,
    samples = samples,
)

fun JobsResponseDto.toDomain(): JobsResponse = JobsResponse(
    enabled = enabled,
    jobs = jobs.map { it.toDomain() },
    intervals = intervals,
)

fun JobDto.toDomain(): Job = Job(
    name = name,
    lastRun = lastRun,
    lastCount = lastCount,
    durationMs = durationMs,
    interval = interval,
    runs = runs,
)

fun VulnResponseDto.toDomain(): VulnResponse = VulnResponse(
    scanning = scanning,
    progress = progress,
    scan = scan?.toDomain(),
    error = error,
)

fun VulnScanDto.toDomain(): VulnScan = VulnScan(
    available = available,
    findings = findings.map { it.toDomain() },
    compared = compared,
    newCount = newCount,
    fixedCount = fixedCount,
    warnings = warnings,
    scannedAt = scannedAt,
)

fun VulnFindingDto.toDomain(): VulnFinding = VulnFinding(
    id = id,
    packageName = packageName,
    installedVersion = installedVersion,
    fixedVersion = fixedVersion,
    severity = severity,
    title = title,
    url = url,
    new = new,
    target = target,
)

fun UsageSourcesResponseDto.toDomain(): UsageSourcesResponse = UsageSourcesResponse(
    sources = sources,
)

fun K8sUsageClusterDto.toDomain(): K8sUsageCluster = K8sUsageCluster(
    nodes = nodes.map { it.toDomain() },
    pods = pods,
)

fun K8sUsageNodeDto.toDomain(): K8sUsageNode = K8sUsageNode(
    name = name,
    ready = ready,
    ip = ip,
    controlPlane = controlPlane,
)

fun ServicesResponseDto.toDomain(): ServicesResponse = ServicesResponse(
    services = services.map { it.toDomain() },
    allowMutations = allowMutations,
)

fun ServiceUnitDto.toDomain(): ServiceUnit = ServiceUnit(
    name = name,
    unit = unit,
    description = description,
    activeState = activeState,
    subState = subState,
    enabled = enabled,
    mainPid = mainPid,
    memoryBytes = memoryBytes,
    since = since,
    installed = installed,
    configFiles = configFiles,
    actions = actions,
)

fun ContainersResponseDto.toDomain(): ContainersResponse = ContainersResponse(
    containers = containers.map { it.toDomain() },
    networks = networks.map { it.toDomain() },
)

fun OsInfoDto.toDomain(): OsInfo? = if (id.isBlank()) null else OsInfo(id = id, name = name, like = like, source = source)

fun ContainerDto.toDomain(): Container = Container(
    id = id,
    name = name,
    image = image,
    state = state,
    status = status,
    project = project,
    serviceName = serviceName,
    ports = ports.map { it.toDomain() },
    networks = networks.map { it.toDomain() },
    declared = declared,
    running = running,
    osInfo = osInfo?.toDomain(),
)

fun ContainerPortDto.toDomain(): ContainerPort = ContainerPort(
    hostIp = hostIp,
    hostPort = hostPort,
    containerPort = containerPort,
    protocol = protocol,
)

fun ContainerNetworkDto.toDomain(): ContainerNetwork = ContainerNetwork(
    name = name,
    ipAddress = ipAddress,
    gateway = gateway,
)

fun DockerNetworkDto.toDomain(): DockerNetwork = DockerNetwork(
    id = id,
    name = name,
    driver = driver,
    scope = scope,
    internal = internal,
    subnets = subnets,
    gateway = gateway,
    bridge = bridge,
)

fun PodmanResponseDto.toDomain(): PodmanResponse = PodmanResponse(
    containers = containers.map { it.toDomain() },
)

fun PodmanContainerDto.toDomain(): PodmanContainer = PodmanContainer(
    id = id,
    name = name,
    image = image,
    state = state,
    status = status,
    osInfo = osInfo?.toDomain(),
)

fun LXDResponseDto.toDomain(): LXDResponse = LXDResponse(
    instances = instances.map { it.toDomain() },
)

fun LXDInstanceDto.toDomain(): LXDInstance = LXDInstance(
    name = name,
    type = type,
    status = status,
    architecture = architecture,
    ipv4 = ipv4,
    osInfo = osInfo?.toDomain(),
)

fun VMsResponseDto.toDomain(): VMsResponse = VMsResponse(
    vms = vms.map { it.toDomain() },
)

fun VirtualMachineDto.toDomain(): VirtualMachine = VirtualMachine(
    name = name,
    uuid = uuid,
    state = state,
    persistent = persistent,
    autostart = autostart,
    vcpus = vcpus,
    memoryKb = memoryKb,
    graphics = graphics,
    osInfo = osInfo?.toDomain(),
)

fun MiscResponseDto.toDomain(): MiscResponse = MiscResponse(
    listeners = listeners.map { it.toDomain() },
)

fun ListenerDto.toDomain(): Listener = Listener(
    protocol = protocol,
    address = address,
    port = port,
    process = process,
    pid = pid,
    command = command,
    user = user,
    unit = unit,
    origin = origin,
)

fun UsersResponseDto.toDomain(): UsersResponse = UsersResponse(
    users = users.map { it.toDomain() },
)

fun UserDto.toDomain(): User = User(
    id = id,
    username = username,
    role = role,
    disabled = disabled,
    createdAt = createdAt,
    lastLoginAt = lastLoginAt,
)

fun ImagesResponseDto.toDomain(): ImagesResponse = ImagesResponse(
    images = images.map { it.toDomain() },
    backupDir = backupDir,
)

fun DockerImageDto.toDomain(): DockerImage = DockerImage(
    id = id,
    tags = tags,
    size = size,
    created = created,
    inUse = inUse,
    usedBy = usedBy,
    dangling = dangling,
)

fun ImageOutcomeDto.toDomain(): ImageOutcome = ImageOutcome(
    ref = ref,
    ok = ok,
    error = error,
    path = path,
)

fun ImageActionResponseDto.toDomain(): ImageActionResponse = ImageActionResponse(
    results = results.map { it.toDomain() },
    reclaimed = reclaimed,
)

fun com.netknownsthat.data.dto.AvailabilityBucketDto.toDomain() = com.netknownsthat.domain.model.AvailabilityBucket(
    bucket = bucket, total = total, ok = ok, uptime = uptime, avgLatencyMs = avgLatencyMs, maxLatencyMs = maxLatencyMs,
)

fun com.netknownsthat.data.dto.MonSeriesDto.toDomain() = com.netknownsthat.domain.model.MonSeries(
    source = source,
    subject = subject,
    metric = metric,
    // A point the hub cannot have produced (short, not a number) is skipped
    // rather than failing the whole chart.
    points = points.mapNotNull { p ->
        val at = (p.getOrNull(0) as? kotlinx.serialization.json.JsonPrimitive)?.content ?: return@mapNotNull null
        fun num(i: Int) = (p.getOrNull(i) as? kotlinx.serialization.json.JsonPrimitive)?.content?.toDoubleOrNull()
        com.netknownsthat.domain.model.MonSeriesPoint(at, num(1) ?: return@mapNotNull null, num(2) ?: 0.0, num(3) ?: 0.0)
    },
)
