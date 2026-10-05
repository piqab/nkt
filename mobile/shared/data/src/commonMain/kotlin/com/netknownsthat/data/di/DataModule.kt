package com.netknownsthat.data.di

import com.netknownsthat.data.local.LocalStore
import com.netknownsthat.data.remote.ApiClient
import com.netknownsthat.data.remote.CertPins
import com.netknownsthat.data.remote.PersistentCookieStore
import com.netknownsthat.data.remote.createHttpEngine
import com.netknownsthat.data.repository.CertificatesRepositoryImpl
import com.netknownsthat.data.repository.ConfigsRepositoryImpl
import com.netknownsthat.data.repository.ContainersRepositoryImpl
import com.netknownsthat.data.repository.DeploymentsRepositoryImpl
import com.netknownsthat.data.repository.EventsRepositoryImpl
import com.netknownsthat.data.repository.FirewallRepositoryImpl
import com.netknownsthat.data.repository.FleetFail2banRepositoryImpl
import com.netknownsthat.data.repository.HostInfoRepositoryImpl
import com.netknownsthat.data.repository.HostMonitoringRepositoryImpl
import com.netknownsthat.data.repository.HostsRepositoryImpl
import com.netknownsthat.data.repository.HubInfoRepositoryImpl
import com.netknownsthat.data.repository.JobsRepositoryImpl
import com.netknownsthat.data.repository.LogsRepositoryImpl
import com.netknownsthat.data.repository.MonitoringRepositoryImpl
import com.netknownsthat.data.repository.SecurityRepositoryImpl
import com.netknownsthat.data.repository.ServicesRepositoryImpl
import com.netknownsthat.data.repository.SessionRepositoryImpl
import com.netknownsthat.data.repository.SettingsRepositoryImpl
import com.netknownsthat.data.repository.SystemRepositoryImpl
import com.netknownsthat.data.repository.TerminalRepositoryImpl
import com.netknownsthat.data.repository.UsersRepositoryImpl
import com.netknownsthat.domain.repository.CertificatesRepository
import com.netknownsthat.domain.repository.ConfigsRepository
import com.netknownsthat.domain.repository.ContainersRepository
import com.netknownsthat.domain.repository.DeploymentsRepository
import com.netknownsthat.domain.repository.EventsRepository
import com.netknownsthat.domain.repository.FirewallRepository
import com.netknownsthat.domain.repository.FleetFail2banRepository
import com.netknownsthat.domain.repository.HostInfoRepository
import com.netknownsthat.domain.repository.HostMonitoringRepository
import com.netknownsthat.domain.repository.HostsRepository
import com.netknownsthat.domain.repository.HubInfoRepository
import com.netknownsthat.domain.repository.JobsRepository
import com.netknownsthat.domain.repository.LogsRepository
import com.netknownsthat.domain.repository.MonitoringRepository
import com.netknownsthat.domain.repository.SecurityRepository
import com.netknownsthat.domain.repository.ServicesRepository
import com.netknownsthat.domain.repository.SessionRepository
import com.netknownsthat.domain.repository.SettingsRepository
import com.netknownsthat.domain.repository.SystemRepository
import com.netknownsthat.domain.repository.TerminalRepository
import com.netknownsthat.domain.repository.UsersRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import org.koin.core.module.Module
import org.koin.core.qualifier.named
import org.koin.dsl.bind
import org.koin.dsl.module

/** Application-wide scope for writes that must outlive a screen. */
val AppScope = named("appScope")

/**
 * Data layer bindings: every domain repository interface to its
 * implementation. The platform module ([platformDataModule]) adds the
 * preferences file location; presentation adds a LanguageProvider.
 */
val dataModule: Module = module {
    single(AppScope) { CoroutineScope(SupervisorJob() + Dispatchers.Default) }
    single { LocalStore(get()) }
    single { CertPins(get(AppScope), get()) }
    single { PersistentCookieStore(get(AppScope), get()) }
    single { ApiClient({ createHttpEngine(get()) }, get(), get(), get()) }

    single { SessionRepositoryImpl(get(), get()) } bind SessionRepository::class
    single { SettingsRepositoryImpl(get(), get(AppScope)) } bind SettingsRepository::class
    single { HostsRepositoryImpl(get()) } bind HostsRepository::class
    single { HubInfoRepositoryImpl(get()) } bind HubInfoRepository::class
    single { EventsRepositoryImpl(get()) } bind EventsRepository::class
    single { MonitoringRepositoryImpl(get()) } bind MonitoringRepository::class
    single { FleetFail2banRepositoryImpl(get()) } bind FleetFail2banRepository::class
    single { DeploymentsRepositoryImpl(get()) } bind DeploymentsRepository::class
    single { HostInfoRepositoryImpl(get()) } bind HostInfoRepository::class
    single { ServicesRepositoryImpl(get()) } bind ServicesRepository::class
    single { ContainersRepositoryImpl(get()) } bind ContainersRepository::class
    single { UsersRepositoryImpl(get()) } bind UsersRepository::class
    single { SecurityRepositoryImpl(get()) } bind SecurityRepository::class
    single { HostMonitoringRepositoryImpl(get()) } bind HostMonitoringRepository::class
    single { ConfigsRepositoryImpl(get()) } bind ConfigsRepository::class
    single { FirewallRepositoryImpl(get()) } bind FirewallRepository::class
    single { CertificatesRepositoryImpl(get()) } bind CertificatesRepository::class
    single { JobsRepositoryImpl(get()) } bind JobsRepository::class
    single { SystemRepositoryImpl(get()) } bind SystemRepository::class
    single { LogsRepositoryImpl(get()) } bind LogsRepository::class
    single { TerminalRepositoryImpl(get()) } bind TerminalRepository::class
}

/** Platform part of the data layer: where the preferences file lives. */
expect fun platformDataModule(): Module
