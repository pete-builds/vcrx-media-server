// Wire types for invited devices and TV pairing in server/api/openapi.yaml.
package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class PairStartRequest(val device: Device)

@Serializable
data class PairStartResponse(
    /** Secret: only this TV holds it, and polls with it. */
    @SerialName("device_code") val deviceCode: String,
    /** What the TV shows, e.g. BCDF-GHJK. */
    @SerialName("user_code") val userCode: String,
    /** The approval page a phone opens; the QR code holds it. */
    @SerialName("verification_url") val verificationUrl: String = "",
    /** A QR code of [verificationUrl], base64 PNG. */
    @SerialName("qr_png") val qrPng: String = "",
    @SerialName("expires_in") val expiresIn: Int = 600,
    val interval: Int = 5,
)

@Serializable
data class PairPollRequest(@SerialName("device_code") val deviceCode: String)

@Serializable
data class PairPollResponse(
    /** "pending" until a phone approves, then "approved" with the session, once. */
    val status: String,
    val interval: Int = 5,
    val token: String? = null,
    val user: User? = null,
)

@Serializable
data class RedeemRequest(val invite: String, val device: Device)
