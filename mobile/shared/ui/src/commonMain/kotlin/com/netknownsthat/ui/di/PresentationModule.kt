package com.netknownsthat.ui.di

import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.repository.LanguageProvider
import com.netknownsthat.domain.usecase.AwaitSettledUseCase
import com.netknownsthat.domain.usecase.CheckNewEventsUseCase
import com.netknownsthat.domain.usecase.LoadAvailabilityUseCase
import com.netknownsthat.domain.usecase.LoadContainersUseCase
import com.netknownsthat.domain.usecase.LoadFirewallUseCase
import com.netknownsthat.domain.usecase.LoadUsageUseCase
import com.netknownsthat.domain.usecase.OpenTerminalUseCase
import com.netknownsthat.domain.usecase.WatchJobUseCase
import com.netknownsthat.ui.host.AuditViewModel
import com.netknownsthat.ui.host.AvailabilityViewModel
import com.netknownsthat.ui.host.CertificatesViewModel
import com.netknownsthat.ui.host.ConfigsViewModel
import com.netknownsthat.ui.host.ContainersViewModel
import com.netknownsthat.ui.host.FindingsViewModel
import com.netknownsthat.ui.host.FirewallViewModel
import com.netknownsthat.ui.host.InterfacesViewModel
import com.netknownsthat.ui.host.LogsViewModel
import com.netknownsthat.ui.host.MiscViewModel
import com.netknownsthat.ui.host.OverviewViewModel
import com.netknownsthat.ui.host.ServicesViewModel
import com.netknownsthat.ui.host.TerminalViewModel
import com.netknownsthat.ui.host.TopologyViewModel
import com.netknownsthat.ui.host.UsageViewModel
import com.netknownsthat.ui.host.UsersViewModel
import com.netknownsthat.ui.host.VulnerabilitiesViewModel
import com.netknownsthat.ui.hosts.HostListViewModel
import com.netknownsthat.ui.hub.ClamAVViewModel
import com.netknownsthat.ui.hub.DeploymentsViewModel
import com.netknownsthat.ui.hub.EventsViewModel
import com.netknownsthat.ui.hub.Fail2banViewModel
import com.netknownsthat.ui.hub.FleetFail2banViewModel
import com.netknownsthat.ui.hub.JobLogViewModel
import com.netknownsthat.ui.hub.JobsViewModel
import com.netknownsthat.ui.hub.MonitoringViewModel
import com.netknownsthat.ui.hub.PipelineViewModel
import com.netknownsthat.ui.hub.RebootViewModel
import com.netknownsthat.ui.i18n.I18n
import com.netknownsthat.ui.session.AboutViewModel
import com.netknownsthat.ui.session.AppViewModel
import com.netknownsthat.ui.session.AuthViewModel
import org.koin.core.module.Module
import org.koin.core.module.dsl.viewModel
import org.koin.dsl.module

/**
 * Presentation bindings: use cases (domain, no platform code) and every
 * screen's ViewModel. Host-level ViewModels take the HostTarget as a
 * parameter — koinViewModel { parametersOf(host) } — so one instance exists
 * per host and section, never a shared one reading "the current host".
 */
val presentationModule: Module = module {
    single<LanguageProvider> { I18n }

    factory { AwaitSettledUseCase() }
    factory { LoadContainersUseCase(get(), get()) }
    factory { LoadAvailabilityUseCase(get()) }
    factory { LoadFirewallUseCase(get()) }
    factory { LoadUsageUseCase(get()) }
    factory { WatchJobUseCase(get()) }
    factory { OpenTerminalUseCase(get()) }
    factory { CheckNewEventsUseCase(get(), get(), get()) }

    viewModel { AppViewModel(get(), get(), get()) }
    viewModel { AuthViewModel(get()) }
    viewModel { AboutViewModel(get(), get(), get()) }
    viewModel { HostListViewModel(get(), get()) }
    viewModel { EventsViewModel(get()) }
    viewModel { MonitoringViewModel(get()) }
    viewModel { FleetFail2banViewModel(get()) }
    viewModel { DeploymentsViewModel(get()) }
    viewModel { (pipelineId: Long) -> PipelineViewModel(pipelineId, get()) }
    viewModel { (owner: JobOwner) -> JobsViewModel(owner, get()) }
    viewModel { (owner: JobOwner, jobId: Long) -> JobLogViewModel(owner, jobId, get(), get()) }

    viewModel { (h: HostTarget) -> OverviewViewModel(h, get()) }
    viewModel { (h: HostTarget) -> FindingsViewModel(h, get()) }
    viewModel { (h: HostTarget) -> InterfacesViewModel(h, get()) }
    viewModel { (h: HostTarget) -> AuditViewModel(h, get()) }
    viewModel { (h: HostTarget) -> MiscViewModel(h, get()) }
    viewModel { (h: HostTarget) -> TopologyViewModel(h, get()) }
    viewModel { (h: HostTarget) -> ServicesViewModel(h, get()) }
    viewModel { (h: HostTarget) -> ContainersViewModel(h, get(), get()) }
    viewModel { (h: HostTarget) -> UsersViewModel(h, get()) }
    viewModel { (h: HostTarget) -> VulnerabilitiesViewModel(h, get()) }
    viewModel { (h: HostTarget) -> AvailabilityViewModel(h, get(), get()) }
    viewModel { (h: HostTarget) -> UsageViewModel(h, get()) }
    viewModel { (h: HostTarget) -> ConfigsViewModel(h, get()) }
    viewModel { (h: HostTarget) -> FirewallViewModel(h, get(), get()) }
    viewModel { (h: HostTarget) -> CertificatesViewModel(h, get()) }
    viewModel { (h: HostTarget) -> LogsViewModel(h, get()) }
    viewModel { (h: HostTarget, btop: Boolean) -> TerminalViewModel(h, btop, get(), get()) }
    viewModel { (h: HostTarget) -> Fail2banViewModel(h, get()) }
    viewModel { (h: HostTarget) -> ClamAVViewModel(h, get()) }
    viewModel { (h: HostTarget) -> RebootViewModel(h, get()) }
}
