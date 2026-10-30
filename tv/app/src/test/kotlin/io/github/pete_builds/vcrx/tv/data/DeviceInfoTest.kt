package io.github.pete_builds.vcrx.tv.data

import org.junit.Assert.assertEquals
import org.junit.Test

class DeviceInfoTest {
    @Test
    fun manufacturerIsNotRepeated() {
        assertEquals("TCL 55S546", DeviceInfo.modelName("TCL", "55S546"))
        assertEquals("TCL Smart TV", DeviceInfo.modelName("TCL", "TCL Smart TV"))
        assertEquals("sdk_google_atv_x86", DeviceInfo.modelName("", "sdk_google_atv_x86"))
        assertEquals("Google", DeviceInfo.modelName("Google", null))
        assertEquals("Android TV", DeviceInfo.modelName(null, null))
    }
}
