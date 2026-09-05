package com.netknownsthat.app.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.ui.about.AboutViewModel
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
        else -> throw IllegalArgumentException("Unknown ViewModel class: $modelClass")
    }
}
