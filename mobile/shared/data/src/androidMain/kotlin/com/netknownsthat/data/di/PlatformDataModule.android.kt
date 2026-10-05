package com.netknownsthat.data.di

import com.netknownsthat.data.local.PREFERENCES_FILE
import com.netknownsthat.data.local.createPreferencesDataStore
import org.koin.android.ext.koin.androidContext
import org.koin.core.module.Module
import org.koin.dsl.module

/** files/datastore/nkt_settings.preferences_pb — where the former app's
 * preferencesDataStore(name = "nkt_settings") kept the same data. */
actual fun platformDataModule(): Module = module {
    single {
        val dir = androidContext().filesDir.resolve("datastore").apply { mkdirs() }
        createPreferencesDataStore(dir.resolve(PREFERENCES_FILE).absolutePath)
    }
}
