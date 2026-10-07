package com.netknownsthat.ui.host

import androidx.compose.runtime.Composable
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.usecase.HostSectionKey
import com.netknownsthat.ui.Section
import com.netknownsthat.ui.hub.ClamAVScreen
import com.netknownsthat.ui.hub.Fail2banScreen
import com.netknownsthat.ui.hub.JobsScreen
import org.koin.compose.viewmodel.koinViewModel
import org.koin.core.parameter.parametersOf

/** One section of a host: its ViewModel (scoped to this back stack entry)
 * and its screen. */
@Composable
fun HostSectionScreen(
    host: HostTarget,
    section: HostSectionKey,
    openSection: (HostSectionKey) -> Unit,
    openContainer: (String) -> Unit,
    openConfigFile: (String) -> Unit,
    openJob: (Long) -> Unit,
    openHostAt: (Long, String) -> Unit,
) {
    val p = { parametersOf(host) }
    when (section) {
        HostSectionKey.OVERVIEW -> Section(koinViewModel<OverviewViewModel>(parameters = p)) { OverviewScreen(it) }
        HostSectionKey.FINDINGS -> Section(koinViewModel<FindingsViewModel>(parameters = p)) { vm ->
            FindingsScreen(vm) { jump ->
                val path = jump.configPath
                if (path != null) openConfigFile(path) else openSection(jump.section)
            }
        }
        HostSectionKey.TERMINAL -> TerminalScreen(koinViewModel { parametersOf(host, false) }, btop = false)
        HostSectionKey.BTOP -> TerminalScreen(koinViewModel { parametersOf(host, true) }, btop = true)
        HostSectionKey.LOGS -> LogsScreen(koinViewModel(parameters = p))
        HostSectionKey.SERVICES -> Section(koinViewModel<ServicesViewModel>(parameters = p)) { ServicesScreen(it) }
        HostSectionKey.CONTAINERS -> Section(koinViewModel<ContainersViewModel>(parameters = p)) { ContainersScreen(it, openContainer, openJob) }
        HostSectionKey.VULNERABILITIES -> Section(koinViewModel<VulnerabilitiesViewModel>(parameters = p)) { VulnerabilitiesScreen(it) }
        HostSectionKey.MALWARE -> Section(koinViewModel<com.netknownsthat.ui.hub.ClamAVViewModel>(parameters = p)) { ClamAVScreen(it, openJob) }
        HostSectionKey.AVAILABILITY -> Section(koinViewModel<AvailabilityViewModel>(parameters = p)) { AvailabilityScreen(it) }
        HostSectionKey.USAGE -> Section(koinViewModel<UsageViewModel>(parameters = p)) { UsageScreen(it) }
        HostSectionKey.CONFIGS -> Section(koinViewModel<ConfigsViewModel>(parameters = p)) { ConfigsScreen(it, openConfigFile) }
        HostSectionKey.FIREWALL -> Section(koinViewModel<FirewallViewModel>(parameters = p)) { FirewallScreen(it) }
        HostSectionKey.CERTIFICATES -> Section(koinViewModel<CertificatesViewModel>(parameters = p)) { CertificatesScreen(it) }
        HostSectionKey.INTERFACES -> Section(koinViewModel<InterfacesViewModel>(parameters = p)) { InterfacesScreen(it) }
        HostSectionKey.MISC -> Section(koinViewModel<MiscViewModel>(parameters = p)) { MiscScreen(it) }
        HostSectionKey.TOPOLOGY -> Section(koinViewModel<TopologyViewModel>(parameters = p)) { TopologyScreen(it) }
        HostSectionKey.USERS -> Section(koinViewModel<UsersViewModel>(parameters = p)) { UsersScreen(it) }
        HostSectionKey.JOBS -> Section(koinViewModel<com.netknownsthat.ui.hub.JobsViewModel> { parametersOf(JobOwner.Host(host)) }) { JobsScreen(it, openJob) }
        HostSectionKey.FAIL2BAN -> Section(koinViewModel<com.netknownsthat.ui.hub.Fail2banViewModel>(parameters = p)) { Fail2banScreen(it) }
        HostSectionKey.AUDIT -> Section(koinViewModel<AuditViewModel>(parameters = p)) { AuditScreen(it) }
    }
}
