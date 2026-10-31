package io.github.pete_builds.vcrx.tv.ui.pair

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.PairPollResponse
import io.github.pete_builds.vcrx.tv.net.PairStartResponse
import io.github.pete_builds.vcrx.tv.net.User
import io.github.pete_builds.vcrx.tv.ui.profiles.StartScreen
import io.github.pete_builds.vcrx.tv.ui.profiles.startScreen
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PairingLogicTest {
    private val start = PairStartResponse(
        deviceCode = "dc", userCode = "BCDF-GHJK", verificationUrl = "http://192.168.1.10:3750/#/pair/BCDFGHJK",
        qrPng = "iVBOR", expiresIn = 600, interval = 5,
    )
    private val t0 = 1_000_000L
    private val showing = PairingLogic.started(start, t0)

    private fun answer(status: String, token: String? = null, user: User? = null, interval: Int = 5) =
        PollOutcome.Answer(PairPollResponse(status = status, interval = interval, token = token, user = user))

    @Test
    fun startingShowsTheCodeAndCountsDownTenMinutes() {
        assertEquals("BCDF-GHJK", showing.userCode)
        assertEquals(t0 + 600_000, showing.expiresAtMs)
        assertEquals(5_000L, showing.intervalMs)
        assertEquals(600L, PairingLogic.secondsLeft(showing, t0))
        assertEquals(1L, PairingLogic.secondsLeft(showing, t0 + 599_500))
    }

    @Test
    fun pendingKeepsPollingUntilTheCodeRunsOut() {
        assertEquals(showing, PairingLogic.afterPoll(showing, answer("pending"), t0 + 5_000))
        assertEquals(PairingState.Expired(PairingLogic.EXPIRED), PairingLogic.afterPoll(showing, answer("pending"), t0 + 600_000))
        assertTrue(PairingLogic.isExpired(showing, t0 + 600_000))
    }

    @Test
    fun approvalSignsInAsTheInvitedDevice() {
        val r = PairingLogic.afterPoll(showing, answer("approved", "tok", User(id = 9, username = "device-ab", displayName = "Den TV")), t0 + 10_000)
        assertEquals(PairingState.Approved("tok", "Den TV"), r)
        // An approval with no session is not a sign-in.
        assertTrue(PairingLogic.afterPoll(showing, answer("approved"), t0) is PairingState.Failed)
        // An approval that lands just as the code runs out still counts: the server issued it.
        assertEquals(PairingState.Approved("tok", "This TV"), PairingLogic.afterPoll(showing, answer("approved", "tok"), t0 + 700_000))
    }

    @Test
    fun aCodeTheServerForgotIsExpiredNotAnError() {
        assertEquals(PairingState.Expired(PairingLogic.GONE), PairingLogic.afterPoll(showing, PollOutcome.Gone, t0 + 5_000))
    }

    @Test
    fun rateLimitedPollsBackOffAndAServerBlipKeepsTheCode() {
        val slower = PairingLogic.afterPoll(showing, PollOutcome.SlowDown, t0) as PairingState.Showing
        assertEquals(10_000L, slower.intervalMs)
        var s: PairingState.Showing = slower
        repeat(10) { s = PairingLogic.afterPoll(s, PollOutcome.SlowDown, t0) as PairingState.Showing }
        assertEquals(PairingLogic.MAX_INTERVAL_MS, s.intervalMs)
        val blip = PairingLogic.afterPoll(showing, PollOutcome.Unreachable("no route"), t0) as PairingState.Showing
        assertEquals("no route", blip.note)
        assertNull((PairingLogic.afterPoll(blip, answer("pending"), t0) as PairingState.Showing).note)
        assertTrue(PairingLogic.afterPoll(showing, PollOutcome.Error("too many"), t0) is PairingState.Failed)
    }

    @Test
    fun theServersIntervalIsBounded() {
        assertEquals(PairingLogic.MIN_INTERVAL_MS, PairingLogic.started(start.copy(interval = 0), t0).intervalMs)
        assertEquals(PairingLogic.MAX_INTERVAL_MS, PairingLogic.started(start.copy(interval = 3600), t0).intervalMs)
        assertEquals(t0, PairingLogic.started(start.copy(expiresIn = -5), t0).expiresAtMs)
    }

    @Test
    fun startResponsesDecode() {
        val r = ApiJson.decodeFromString(
            PairStartResponse.serializer(),
            """{"device_code":"d","user_code":"BCDF-GHJK","verification_url":"u","qr_png":"q","expires_in":600,"interval":5,"extra":1}""",
        )
        assertEquals("BCDF-GHJK", r.userCode)
        val p = ApiJson.decodeFromString(
            PairPollResponse.serializer(),
            """{"status":"approved","token":"t","expires_at":1,"user":{"id":3,"username":"device-ab","display_name":"Den TV","is_admin":false}}""",
        )
        assertEquals("t", p.token)
        assertEquals("Den TV", p.user?.displayName)
    }

    @Test
    fun invitesAreTypedOrPastedAsTheirLink() {
        val secret = "abcdefghijklmnopqrstuvwxyz234567"
        assertEquals(secret, PairingLogic.normalizeInvite(secret))
        assertEquals(secret, PairingLogic.normalizeInvite(" ABCD-EFGH IJKL-MNOP qrst uvwx yz23 4567 "))
        assertEquals(secret, PairingLogic.normalizeInvite("http://192.168.1.10:3750/#/invite/$secret"))
        assertNull(PairingLogic.normalizeInvite(""))
        assertNull(PairingLogic.normalizeInvite(secret.dropLast(1)))
        assertNull(PairingLogic.normalizeInvite(secret + "a"))
        assertNull(PairingLogic.normalizeInvite("abcdefghijklmnopqrstuvwxyz234561"))
    }

    @Test
    fun anInvitedDeviceNeverOpensOnThePicker() {
        assertEquals(StartScreen.PICKER, startScreen(hasServer = true, hasToken = true, alwaysAsk = true))
        assertEquals(StartScreen.BROWSE, startScreen(hasServer = true, hasToken = true, alwaysAsk = true, invited = true))
        assertEquals(StartScreen.LOGIN, startScreen(hasServer = true, hasToken = false, alwaysAsk = true, invited = true))
    }
}
