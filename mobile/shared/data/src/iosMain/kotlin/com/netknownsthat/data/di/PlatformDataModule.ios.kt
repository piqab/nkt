package com.netknownsthat.data.di

import com.netknownsthat.data.local.PREFERENCES_FILE
import com.netknownsthat.data.local.createPreferencesDataStore
import kotlinx.cinterop.ExperimentalForeignApi
import org.koin.core.module.Module
import org.koin.dsl.module
import platform.Foundation.NSDocumentDirectory
import platform.Foundation.NSFileManager
import platform.Foundation.NSURL
import platform.Foundation.NSUserDomainMask

@OptIn(ExperimentalForeignApi::class)
private fun documentsPath(): String {
    val url: NSURL? = NSFileManager.defaultManager.URLForDirectory(
        directory = NSDocumentDirectory,
        inDomain = NSUserDomainMask,
        appropriateForURL = null,
        create = true,
        error = null,
    )
    return requireNotNull(url?.path) { "no documents directory" }
}

actual fun platformDataModule(): Module = module {
    single { createPreferencesDataStore(documentsPath() + "/" + PREFERENCES_FILE) }
}
