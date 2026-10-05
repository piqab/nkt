package com.netknownsthat.data.remote

import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.engine.darwin.Darwin
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.addressOf
import kotlinx.cinterop.convert
import kotlinx.cinterop.usePinned
import platform.CoreCrypto.CC_SHA256
import platform.CoreCrypto.CC_SHA256_DIGEST_LENGTH
import platform.CoreFoundation.CFDataGetBytePtr
import platform.CoreFoundation.CFDataGetLength
import platform.CoreFoundation.CFRelease
import platform.Foundation.NSURLAuthenticationMethodServerTrust
import platform.Foundation.NSURLCredential
import platform.Foundation.NSURLSessionAuthChallengeCancelAuthenticationChallenge
import platform.Foundation.NSURLSessionAuthChallengePerformDefaultHandling
import platform.Foundation.NSURLSessionAuthChallengeUseCredential
import platform.Foundation.credentialForTrust
import platform.Foundation.serverTrust
import platform.Security.SecCertificateCopyData
import platform.Security.SecTrustEvaluateWithError
import platform.Security.SecTrustGetCertificateAtIndex
import platform.Security.SecTrustRef

/** SHA-256 of the leaf certificate's DER bytes, lowercase hex — the same
 * fingerprint the Android engine and the hub's About screen use. */
@OptIn(ExperimentalForeignApi::class)
private fun leafSha256(trust: SecTrustRef): String? {
    val cert = SecTrustGetCertificateAtIndex(trust, 0) ?: return null
    val data = SecCertificateCopyData(cert) ?: return null
    try {
        val length = CFDataGetLength(data)
        val bytes = CFDataGetBytePtr(data) ?: return null
        val digest = UByteArray(CC_SHA256_DIGEST_LENGTH)
        digest.usePinned { pinned ->
            CC_SHA256(bytes, length.convert(), pinned.addressOf(0))
        }
        return digest.joinToString("") { it.toString(16).padStart(2, '0') }
    } finally {
        CFRelease(data)
    }
}

@OptIn(ExperimentalForeignApi::class)
actual fun createHttpEngine(pins: CertPins): HttpClientEngine = Darwin.create {
    // System trust first (a hub behind a proxy with a real certificate);
    // a self-signed one is checked against the pin.
    handleChallenge { _, _, challenge, completionHandler ->
        val space = challenge.protectionSpace
        if (space.authenticationMethod != NSURLAuthenticationMethodServerTrust) {
            completionHandler(NSURLSessionAuthChallengePerformDefaultHandling, null)
            return@handleChallenge
        }
        val trust = space.serverTrust
        if (trust == null) {
            completionHandler(NSURLSessionAuthChallengeCancelAuthenticationChallenge, null)
            return@handleChallenge
        }
        if (SecTrustEvaluateWithError(trust, null)) {
            completionHandler(NSURLSessionAuthChallengePerformDefaultHandling, null)
            return@handleChallenge
        }
        val authority = pins.currentAuthority
        val fingerprint = leafSha256(trust)
        if (authority != null && fingerprint != null && pins.verify(authority, fingerprint)) {
            completionHandler(NSURLSessionAuthChallengeUseCredential, NSURLCredential.credentialForTrust(trust))
        } else {
            completionHandler(NSURLSessionAuthChallengeCancelAuthenticationChallenge, null)
        }
    }
}
