// Wire types for household profiles ("Who's watching?") in server/api/openapi.yaml.
package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class Profile(
    val id: Long,
    val name: String,
    @SerialName("is_admin") val isAdmin: Boolean = false,
    val kid: Boolean = false,
    @SerialName("has_pin") val hasPin: Boolean = false,
    /** Background for the initials, "#rrggbb". */
    val color: String = "",
    /** The uploaded picture, a path on the server; null draws the initials. */
    @SerialName("avatar_url") val avatarUrl: String? = null,
    /** What switching to this profile asks for: "none", "pin" or "password". */
    val needs: String = NEEDS_NONE,
) {
    companion object {
        const val NEEDS_NONE = "none"
        const val NEEDS_PIN = "pin"
        const val NEEDS_PASSWORD = "password"
    }
}

@Serializable
data class ProfilesResponse(
    val profiles: List<Profile> = emptyList(),
    /** This device's own profile; null in the picker before anyone is chosen. */
    @SerialName("current_id") val currentId: Long? = null,
    /** Where the server suggests focus starts. */
    @SerialName("focus_id") val focusId: Long? = null,
)

@Serializable
data class SwitchRequest(
    @SerialName("user_id") val userId: Long,
    val pin: String? = null,
    val password: String? = null,
    val device: Device,
)
