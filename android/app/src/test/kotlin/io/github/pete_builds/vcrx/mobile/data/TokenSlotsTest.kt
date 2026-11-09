package io.github.pete_builds.vcrx.mobile.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TokenSlotsTest {
    @Test
    fun slotsNameWhoSignedIn() {
        assertEquals("", TokenSlots.forProfile(null))
        assertEquals("profile:7", TokenSlots.forProfile(7))
        assertEquals(7L, TokenSlots.profileId("profile:7"))
        assertNull(TokenSlots.profileId(""))
        assertNull(TokenSlots.profileId("device"))
        assertTrue(TokenSlots.isInvitedDevice(TokenSlots.DEVICE))
        assertFalse(TokenSlots.isInvitedDevice("profile:7"))
    }

    @Test
    fun theUnnamedSlotKeepsTheTvKeyNames() {
        assertEquals("iv", TokenSlots.ivKey(""))
        assertEquals("token", TokenSlots.tokenKey(""))
        assertEquals("token.profile:7", TokenSlots.tokenKey("profile:7"))
        assertEquals("iv.device", TokenSlots.ivKey("device"))
    }
}
