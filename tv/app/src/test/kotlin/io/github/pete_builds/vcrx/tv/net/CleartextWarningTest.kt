package io.github.pete_builds.vcrx.tv.net

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Which addresses the Connect screen warns about: plain http:// to a host that is
 * not on a private network. Inputs go through [ServerAddress.normalize] first, as
 * on the screen, so the hosts are in the form OkHttp gives them.
 */
class CleartextWarningTest {
    private fun warns(input: String): Boolean =
        ServerAddress.cleartextToPublicHost(ServerAddress.normalize(input) ?: error("not an address: $input"))

    @Test
    fun privateAddressesDoNotWarn() {
        val private = listOf(
            "10.0.0.5", "10.255.255.255",
            "172.16.0.1", "172.31.255.255",
            "192.168.1.20:3750", "http://192.168.0.20:3750",
            "127.0.0.1", "127.8.9.10",
            "169.254.10.10",
            "100.64.0.1", "100.101.102.103", "100.127.255.255",
            "[::1]", "[fe80::1]", "[febf::1]",
            "[fd12:3456:789a::1]", "[fc00::1]",
            "[::ffff:192.168.1.20]",
            "nas.local", "NAS.Local", "living-room.local:8080",
            "localhost", "nix1", "nix1:3750", "media.home.arpa",
        )
        for (input in private) assertFalse(input, warns(input))
    }

    @Test
    fun publicAddressesOverHttpWarn() {
        val public = listOf(
            "8.8.8.8", "http://203.0.113.7:3750",
            "11.0.0.1", "9.255.255.255",
            "172.15.255.255", "172.32.0.1",
            "192.169.0.1", "192.167.255.255",
            "169.253.1.1", "128.0.0.1",
            "100.63.255.255", "100.128.0.1",
            "[2001:db8::1]", "[fe00::1]", "[fec0::1]", "[fe7f::1]", "[fb00::1]",
            "[::ffff:8.8.8.8]",
            "media.example.com", "http://media.example.com/vcrx",
            "local.example.com", "example.localdomain.net", "nix1.tail1234.ts.net",
        )
        for (input in public) assertTrue(input, warns(input))
    }

    @Test
    fun httpsNeverWarns() {
        for (input in listOf("https://media.example.com", "https://8.8.8.8:8443", "https://192.168.1.20")) {
            assertFalse(input, warns(input))
        }
    }

    @Test
    fun hostsAreJudgedByFormWithoutALookup() {
        // A name that is not an address is never resolved: these would only be private
        // if a resolver said so, and the check must not ask one from the main thread.
        assertFalse(ServerAddress.isPrivateHost("example.com"))
        assertFalse(ServerAddress.isPrivateHost("not:an:address:zz"))
        assertFalse(ServerAddress.isPrivateHost(""))
        assertTrue(ServerAddress.isPrivateHost("NAS.LOCAL."))
        assertFalse(ServerAddress.isPrivateHost("256.1.1.1.example"))
    }
}
