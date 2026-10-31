package io.github.pete_builds.vcrx.tv.net

import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ServerAddressTest {
    @Test
    fun bareHostGetsHttpAndTheDefaultPort() {
        assertEquals("http://192.168.1.20:3750", ServerAddress.normalize("192.168.1.20"))
        assertEquals("http://192.168.1.20:3750", ServerAddress.normalize("  192.168.1.20:3750  "))
        assertEquals("http://nix1:3750", ServerAddress.normalize("nix1"))
        assertEquals("http://[fe80::1]:3750", ServerAddress.normalize("[fe80::1]"))
    }

    @Test
    fun explicitPortsAndSchemesAreKept() {
        assertEquals("http://192.168.1.20:8080", ServerAddress.normalize("http://192.168.1.20:8080/"))
        assertEquals("https://media.example.com", ServerAddress.normalize("https://media.example.com"))
        assertEquals("https://media.example.com/vcrx", ServerAddress.normalize("https://media.example.com/vcrx/"))
        // Port 80 is the http default, so the canonical form drops it.
        assertEquals("http://host", ServerAddress.normalize("http://host:80"))
    }

    @Test
    fun junkIsRejected() {
        assertNull(ServerAddress.normalize(""))
        assertNull(ServerAddress.normalize("   "))
        assertNull(ServerAddress.normalize("http://"))
        assertNull(ServerAddress.normalize("ftp://host"))
        assertNull(ServerAddress.normalize("two words"))
    }

    @Test
    fun resolvesServerPathsAgainstTheBase() {
        assertEquals(
            "http://192.168.1.20:3750/api/v1/versions/3/file?session=abc",
            ServerAddress.resolve("http://192.168.1.20:3750", "/api/v1/versions/3/file?session=abc").toString(),
        )
        assertEquals(
            "https://media.example.com/vcrx/api/v1/server",
            ServerAddress.resolve("https://media.example.com/vcrx", "api/v1/server").toString(),
        )
    }

    @Test
    fun tokenOnlyGoesToTheSameOrigin() {
        val base = "http://192.168.1.20:3750".toHttpUrl()
        assertTrue(ServerAddress.sameOrigin("http://192.168.1.20:3750/api/v1/home".toHttpUrl(), base))
        assertFalse(ServerAddress.sameOrigin("http://192.168.1.20:3751/x".toHttpUrl(), base))
        assertFalse(ServerAddress.sameOrigin("https://192.168.1.20:3750/x".toHttpUrl(), base))
        assertFalse(ServerAddress.sameOrigin("http://example.com:3750/x".toHttpUrl(), base))
    }
}
