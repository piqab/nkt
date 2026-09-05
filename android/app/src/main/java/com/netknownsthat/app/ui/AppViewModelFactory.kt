package com.netknownsthat.app.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.ui.about.AboutViewModel
import com.netknownsthat.app.ui.host.AuditViewModel
import com.netknownsthat.app.ui.host.AvailabilityViewModel
import com.netknownsthat.app.ui.host.CertificatesViewModel
import com.netknownsthat.app.ui.host.ConfigsViewModel
import com.netknownsthat.app.ui.host.ContainersViewModel
import com.netknownsthat.app.ui.host.FindingsViewModel
import com.netknownsthat.app.ui.host.FirewallViewModel
import com.netknownsthat.app.ui.host.InterfacesViewModel
import com.netknownsthat.app.ui.host.MiscViewModel
import com.netknownsthat.app.ui.host.OverviewViewModel
import com.netknownsthat.app.ui.host.ServicesViewModel
import com.netknownsthat.app.ui.host.TerminalViewModel
import com.netknownsthat.app.ui.host.TopologyViewModel
import com.netknownsthat.app.ui.host.UsageViewModel
import com.netknownsthat.app.ui.host.UsersViewModel
import com.netknownsthat.app.ui.host.VulnerabilitiesViewModel
import com.netknownsthat.app.ui.hosts.HostListViewModel
import com.netknownsthat.app.ui.login.AuthViewModel

/**
 * Hand-written factory in place of Hilt/Koin — the only dependency any
 * screen's ViewModel needs so far is the single shared [HubClient] (see
 * NktApplication). Add a case here for each new ViewModel as later phases
 * introduce them.
 */
class AppViewModelFactory(private val hubClient: HubClient) : ViewModelProvider.Factory {
    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T = when (modelClass) {
        AuthViewModel::class.java -> AuthViewModel(hubClient) as T
        HostListViewModel::class.java -> HostListViewModel(hubClient) as T
        AboutViewModel::class.java -> AboutViewModel(hubClient) as T
        OverviewViewModel::class.java -> OverviewViewModel(hubClient) as T
        FindingsViewModel::class.java -> FindingsViewModel(hubClient) as T
        InterfacesViewModel::class.java -> InterfacesViewModel(hubClient) as T
        AuditViewModel::class.java -> AuditViewModel(hubClient) as T
        ServicesViewModel::class.java -> ServicesViewModel(hubClient) as T
        ContainersViewModel::class.java -> ContainersViewModel(hubClient) as T
        UsersViewModel::class.java -> UsersViewModel(hubClient) as T
        MiscViewModel::class.java -> MiscViewModel(hubClient) as T
        VulnerabilitiesViewModel::class.java -> VulnerabilitiesViewModel(hubClient) as T
        AvailabilityViewModel::class.java -> AvailabilityViewModel(hubClient) as T
        UsageViewModel::class.java -> UsageViewModel(hubClient) as T
        ConfigsViewModel::class.java -> ConfigsViewModel(hubClient) as T
        FirewallViewModel::class.java -> FirewallViewModel(hubClient) as T
        CertificatesViewModel::class.java -> CertificatesViewModel(hubClient) as T
        TopologyViewModel::class.java -> TopologyViewModel(hubClient) as T
        TerminalViewModel::class.java -> TerminalViewModel(hubClient) as T
        else -> throw IllegalArgumentException("Unknown ViewModel class: $modelClass")
    }
}
