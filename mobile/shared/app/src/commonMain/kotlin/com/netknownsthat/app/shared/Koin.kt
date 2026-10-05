package com.netknownsthat.app.shared

import com.netknownsthat.data.di.dataModule
import com.netknownsthat.data.di.platformDataModule
import com.netknownsthat.ui.di.presentationModule
import org.koin.core.KoinApplication
import org.koin.core.context.startKoin
import org.koin.core.module.Module
import org.koin.dsl.KoinAppDeclaration

/**
 * The composition root: data implementations bound to domain interfaces,
 * use cases and ViewModels, plus what the platform app provides (its
 * PlatformServices). The only place that knows both data and presentation.
 */
fun appModules(platform: Module): List<Module> =
    listOf(dataModule, platformDataModule(), presentationModule, platform)

fun initKoin(platform: Module, declaration: KoinAppDeclaration = {}): KoinApplication =
    startKoin {
        declaration()
        modules(appModules(platform))
    }
