package io.github.pete_builds.vcrx.tv.ui.profiles

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.Profile
import io.github.pete_builds.vcrx.tv.net.ProfilesResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PickerLogicTest {
    private val pete = Profile(id = 1, name = "pete", isAdmin = true, needs = Profile.NEEDS_PASSWORD)
    private val robin = Profile(id = 2, name = "Robin", kid = true)
    private val sam = Profile(id = 3, name = "Sam", hasPin = true, needs = Profile.NEEDS_PIN)

    @Test
    fun aProfileWithoutAPinSwitchesAtOnce() {
        val r = PickerLogic.choose(robin)
        assertEquals(PickerEffect.Switch(robin), r.effect)
        assertEquals(PickerStep.Grid(robin.id), r.step)
    }

    @Test
    fun aProfileWithAPinOpensThePadAndNeverSwitchesWithoutIt() {
        val r = PickerLogic.choose(sam)
        assertEquals(PickerStep.Pin(sam), r.step)
        assertEquals(PickerEffect.None, r.effect)
        // OK with nothing entered, or too few digits, sends nothing.
        var pad = r.step as PickerStep.Pin
        assertEquals(PickerEffect.None, PickerLogic.ok(pad).effect)
        for (d in "246") pad = PickerLogic.digit(pad, d).step as PickerStep.Pin
        val short = PickerLogic.ok(pad)
        assertEquals(PickerEffect.None, short.effect)
        assertEquals("Enter at least 4 digits.", (short.step as PickerStep.Pin).error)
        pad = PickerLogic.digit(short.step as PickerStep.Pin, '8').step as PickerStep.Pin
        assertNull("typing clears the error", pad.error)
        val sent = PickerLogic.ok(pad)
        assertEquals(PickerEffect.Switch(sam, pin = "2468"), sent.effect)
        assertTrue((sent.step as PickerStep.Pin).busy)
    }

    @Test
    fun anAdminWithNoPinAsksForThePassword() {
        val r = PickerLogic.choose(pete)
        assertEquals(PickerStep.Password(pete), r.step)
        val step = r.step as PickerStep.Password
        assertEquals(PickerEffect.None, PickerLogic.password(step, "").effect)
        assertEquals(PickerEffect.Switch(pete, password = "secret-pass"), PickerLogic.password(step, "secret-pass").effect)
    }

    @Test
    fun digitsDeleteAndTheEighthDigitSubmits() {
        var pad = PickerStep.Pin(sam)
        pad = PickerLogic.digit(pad, 'x').step as PickerStep.Pin
        assertEquals("only digits count", "", pad.digits)
        for (d in "1234") pad = PickerLogic.digit(pad, d).step as PickerStep.Pin
        pad = PickerLogic.delete(pad).step as PickerStep.Pin
        assertEquals("123", pad.digits)
        for (d in "4567") pad = PickerLogic.digit(pad, d).step as PickerStep.Pin
        val eighth = PickerLogic.digit(pad, '8')
        assertEquals(PickerEffect.Switch(sam, pin = "12345678"), eighth.effect)
        // While the server checks, presses are ignored, so a PIN is never sent twice.
        val busy = eighth.step as PickerStep.Pin
        assertEquals(busy, PickerLogic.digit(busy, '9').step)
        assertEquals(PickerEffect.None, PickerLogic.ok(busy).effect)
        assertEquals(busy, PickerLogic.delete(busy).step)
        // The same after OK on a four-digit PIN, where there is room for more digits.
        val sentFour = PickerLogic.ok(PickerStep.Pin(sam, digits = "2468")).step as PickerStep.Pin
        assertTrue(sentFour.busy)
        assertEquals(sentFour, PickerLogic.digit(sentFour, '1').step)
        assertEquals(PickerEffect.None, PickerLogic.digit(sentFour, '1').effect)
    }

    @Test
    fun aWrongPinClearsThePadForAnotherTry() {
        val busy = PickerStep.Pin(sam, digits = "1111", busy = true)
        val after = PickerLogic.failed(busy, sam, "wrong_pin", "That PIN is not right.") as PickerStep.Pin
        assertEquals("", after.digits)
        assertEquals("That PIN is not right.", after.error)
        assertTrue(!after.busy)
        val limited = PickerLogic.failed(busy, sam, "rate_limited", "Too many wrong PINs.") as PickerStep.Pin
        assertEquals("Too many wrong PINs.", limited.error)
    }

    @Test
    fun aProfileThatGainedAPinSinceTheListLoadedGetsThePad() {
        // The list said "none", the server says a PIN is needed now.
        val stale = robin.copy(hasPin = true)
        assertEquals(PickerStep.Pin(stale), PickerLogic.failed(PickerStep.Grid(robin.id), stale, "pin_required", "Enter the PIN"))
        assertEquals(PickerStep.Password(pete), PickerLogic.failed(PickerStep.Grid(pete.id), pete, "credential_required", "x"))
        assertEquals(PickerStep.Grid(robin.id, "Server down"), PickerLogic.failed(PickerStep.Grid(robin.id), robin, "http_500", "Server down"))
    }

    @Test
    fun backReturnsToTheSameAvatarThenLeaves() {
        val grid = PickerLogic.back(PickerStep.Pin(sam, digits = "12"))
        assertEquals(PickerStep.Grid(sam.id), grid.step)
        assertEquals(PickerEffect.None, grid.effect)
        assertEquals(PickerStep.Grid(pete.id), PickerLogic.back(PickerStep.Password(pete)).step)
        assertEquals(PickerEffect.Exit, PickerLogic.back(grid.step).effect)
    }

    @Test
    fun focusStartsOnThisDeviceThenTheLastProfileThenTheServersChoice() {
        val list = listOf(pete, robin, sam)
        assertEquals(3L, PickerLogic.initialFocus(ProfilesResponse(list, currentId = 3, focusId = 1), lastProfileId = 2))
        assertEquals(2L, PickerLogic.initialFocus(ProfilesResponse(list, currentId = null, focusId = 1), lastProfileId = 2))
        assertEquals(1L, PickerLogic.initialFocus(ProfilesResponse(list, focusId = 1), lastProfileId = 99))
        assertEquals(1L, PickerLogic.initialFocus(ProfilesResponse(list), lastProfileId = null))
        assertNull(PickerLogic.initialFocus(ProfilesResponse(emptyList(), focusId = 1), lastProfileId = 1))
    }

    @Test
    fun initialsAndColours() {
        assertEquals("PE", PickerLogic.initials("pete"))
        assertEquals("RS", PickerLogic.initials("  Robin  Smith "))
        assertEquals("A", PickerLogic.initials("a"))
        assertEquals("?", PickerLogic.initials(" "))
        assertEquals(0xFF1D4ED8L, PickerLogic.parseColor("#1d4ed8"))
        assertNull(PickerLogic.parseColor("red"))
        assertNull(PickerLogic.parseColor(""))
    }

    @Test
    fun alwaysAskOpensThePickerOnlyWithASession() {
        assertEquals(StartScreen.CONNECT, startScreen(hasServer = false, hasToken = true, alwaysAsk = true))
        assertEquals(StartScreen.LOGIN, startScreen(hasServer = true, hasToken = false, alwaysAsk = true))
        assertEquals(StartScreen.PICKER, startScreen(hasServer = true, hasToken = true, alwaysAsk = true))
        assertEquals(StartScreen.BROWSE, startScreen(hasServer = true, hasToken = true, alwaysAsk = false))
    }

    @Test
    fun decodesTheServersProfileList() {
        val body = """{"profiles":[{"id":1,"name":"pete","is_admin":true,"kid":false,"has_pin":false,"color":"#c2410c",
            "avatar_url":null,"needs":"password"},{"id":2,"name":"Robin","kid":true,"color":"#1d4ed8",
            "avatar_url":"/api/v1/profiles/2/avatar?v=5","needs":"none","new_field":1}],"current_id":null,"focus_id":1}"""
        val r = ApiJson.decodeFromString(ProfilesResponse.serializer(), body)
        assertEquals(listOf(1L, 2L), r.profiles.map { it.id })
        assertEquals(Profile.NEEDS_PASSWORD, r.profiles[0].needs)
        assertEquals("/api/v1/profiles/2/avatar?v=5", r.profiles[1].avatarUrl)
        assertNull(r.currentId)
        assertEquals(1L, r.focusId)
    }
}
