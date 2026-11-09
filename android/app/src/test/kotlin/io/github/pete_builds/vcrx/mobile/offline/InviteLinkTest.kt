package io.github.pete_builds.vcrx.mobile.offline

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class InviteLinkTest {
    @Test
    fun readsTheLinkSettingsCopies() {
        assertEquals(InviteLink("https://media.example.com", "AbC123xyz_-9"), InviteLink.parse(" https://media.example.com/#/invite/AbC123xyz_-9 "))
        assertEquals(InviteLink("http://192.168.1.20:3750", "secret12345"), InviteLink.parse("http://192.168.1.20:3750/#/invite/secret12345"))
    }

    @Test
    fun aBareSecretKeepsTheServerAlreadyChosen() {
        assertEquals(InviteLink(null, "secret12345"), InviteLink.parse("secret12345"))
    }

    @Test
    fun refusesWhatIsNotAnInvite() {
        assertNull(InviteLink.parse(""))
        assertNull(InviteLink.parse("https://media.example.com/#/invite/"))
        assertNull(InviteLink.parse("not an invite"))
        assertNull(InviteLink.parse("short"))
    }
}
