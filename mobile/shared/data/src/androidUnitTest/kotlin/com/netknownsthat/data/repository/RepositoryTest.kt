package com.netknownsthat.data.repository

import com.netknownsthat.data.local.LocalStore
import com.netknownsthat.data.local.createPreferencesDataStore
import com.netknownsthat.data.remote.ApiClient
import com.netknownsthat.data.remote.CertPins
import com.netknownsthat.data.remote.PersistentCookieStore
import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.repository.AppLanguage
import com.netknownsthat.domain.repository.InstallStart
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.HttpRequestData
import io.ktor.client.request.HttpResponseData
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.Url
import io.ktor.http.headersOf
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.nio.file.Files

/** Repositories against a fake hub: paths, headers, error mapping, cookies. */
class RepositoryTest {
    private val requests = mutableListOf<HttpRequestData>()

    private fun api(
        language: AppLanguage = AppLanguage.EN,
        handler: suspend MockRequestHandleScope.(HttpRequestData) -> HttpResponseData,
    ): ApiClient {
        val dir = Files.createTempDirectory("nkt").toFile()
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val local = LocalStore(createPreferencesDataStore(File(dir, "p.preferences_pb").absolutePath))
        val engine = MockEngine { request ->
            requests += request
            handler(request)
        }
        return ApiClient({ engine }, PersistentCookieStore(scope, local), CertPins(scope, local), { language })
            .also { it.setBase(Url("http://hub.test:8077")) }
    }

    private val jsonHeaders = headersOf(HttpHeaders.ContentType, "application/json")

    @Test
    fun hostCallsAreScopedByPath() = runBlocking {
        val api = api { respond("""{"findings":[],"counts":{}}""", headers = jsonHeaders) }
        HostInfoRepositoryImpl(api).findings(HostTarget(3))
        HostInfoRepositoryImpl(api).findings(HostTarget.LOCAL)
        assertEquals("/api/hosts/3/findings", requests[0].url.encodedPath)
        assertEquals("/api/hosts/local/findings", requests[1].url.encodedPath)
        assertEquals("en", requests[0].headers["X-NKT-Lang"])
    }

    @Test
    fun hubJobsLiveOnTheHubMachine() = runBlocking {
        val api = api { respond("""{"jobs":[],"total":0}""", headers = jsonHeaders) }
        JobsRepositoryImpl(api).jobs(JobOwner.Hub, "", 10)
        JobsRepositoryImpl(api).jobs(JobOwner.Host(HostTarget(7)), "failed", 10)
        assertEquals("/api/hosts/local/jobs", requests[0].url.encodedPath)
        assertEquals("/api/hosts/7/jobs", requests[1].url.encodedPath)
        assertEquals("failed", requests[1].url.parameters["status"])
    }

    @Test
    fun errorsAreTyped() = runBlocking {
        val api = api { request ->
            when (request.url.encodedPath) {
                "/api/auth/me" -> respond("""{"error":"sign in"}""", HttpStatusCode.Unauthorized, jsonHeaders)
                else -> respond("""{"error":"nkt is already installed"}""", HttpStatusCode.Conflict, jsonHeaders)
            }
        }
        val me = SessionRepositoryImpl(api, LocalStore(createPreferencesDataStore(Files.createTempFile("p", ".preferences_pb").toFile().apply { delete() }.absolutePath))).me()
        assertEquals(AppError.Unauthorized("sign in"), (me as Outcome.Failure).error)
        val install = HostsRepositoryImpl(api).install(5, force = false)
        assertEquals(InstallStart.ForeignInstall("nkt is already installed"), (install as Outcome.Success).value)
    }

    @Test
    fun badShapeIsBadResponse() = runBlocking {
        val api = api { respond("""{"findings": 42}""", headers = jsonHeaders) }
        val r = HostInfoRepositoryImpl(api).findings(HostTarget(1))
        assertTrue((r as Outcome.Failure).error is AppError.BadResponse)
    }

    @Test
    fun sessionCookieIsSentBack() = runBlocking {
        val api = api { request ->
            if (request.url.encodedPath == "/api/auth/login") {
                respond("{}", headers = headersOf(HttpHeaders.SetCookie, "nkt_session=abc; Path=/; HttpOnly"))
            } else {
                respond("""{"username":"admin","role":"admin","is_admin":true,"mode":"hub","allow_mutations":true,"simulated":false}""", headers = jsonHeaders)
            }
        }
        val dir = Files.createTempDirectory("nkt").toFile()
        val session = SessionRepositoryImpl(api, LocalStore(createPreferencesDataStore(File(dir, "s.preferences_pb").absolutePath)))
        val me = session.login("admin", "pw")
        assertEquals("admin", (me as Outcome.Success).value.username)
        assertTrue(requests.last().headers[HttpHeaders.Cookie].orEmpty().contains("nkt_session=abc"))
    }
}
