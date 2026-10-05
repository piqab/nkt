package com.netknownsthat.app

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import com.netknownsthat.ui.HostJump
import com.netknownsthat.ui.NktApp
import kotlinx.coroutines.flow.MutableSharedFlow

/** Shows the shared Compose UI; notification taps arrive as host jumps. */
class MainActivity : ComponentActivity() {
    private val jumps = MutableSharedFlow<HostJump>(replay = 1, extraBufferCapacity = 4)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        if (savedInstanceState == null) takeJump(intent)
        setContent { NktApp(jumps) }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        takeJump(intent)
    }

    private fun takeJump(intent: Intent?) {
        val id = intent?.getLongExtra(EventsWorker.EXTRA_HOST_ID, 0L) ?: 0L
        if (id != 0L) jumps.tryEmit(HostJump(id, intent?.getStringExtra(EventsWorker.EXTRA_PATH) ?: "/"))
    }
}
