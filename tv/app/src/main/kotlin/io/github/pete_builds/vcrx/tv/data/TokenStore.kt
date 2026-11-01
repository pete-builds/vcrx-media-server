package io.github.pete_builds.vcrx.tv.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.util.Log
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * The bearer token, encrypted with an AES-GCM key that never leaves the Android
 * Keystore. Only the IV and ciphertext reach private preferences, so a copy of the
 * app's data without the device's keystore is useless. The decrypted token is held
 * in memory because the interceptor reads it on every request.
 *
 * Tokens are kept per household profile, in a slot named for the profile, and the
 * active slot is remembered so the app reopens as the last profile. A password or
 * automatic sign-in uses the unnamed slot, as before profiles existed. When the
 * device switches profile the server ends the old session, so the slot it left is
 * dropped: a token kept for a profile with a PIN would let anyone holding the
 * remote back in without it.
 */
class TokenStore(context: Context) {
    private val prefs = context.getSharedPreferences("auth", Context.MODE_PRIVATE)

    @Volatile
    private var cached: String? = null

    @Volatile
    private var loaded = false

    /** Called after every save and sign-out: the session the app acts for changed. */
    @Volatile
    var onSessionChange: (() -> Unit)? = null

    /** The active profile's token. */
    fun token(): String? {
        if (loaded) return cached
        synchronized(this) {
            if (!loaded) {
                cached = decrypt(activeSlot())
                loaded = true
            }
            return cached
        }
    }

    /** The household profile this device is signed in as, when it signed in by switching. */
    val activeProfileId: Long?
        get() = activeSlot().removePrefix(PROFILE_PREFIX).toLongOrNull()

    /**
     * True when this device signed in by an invite or by pairing with a phone. It
     * is then its own principal: the app never offers it the profile picker.
     */
    val isInvitedDevice: Boolean
        get() = activeSlot() == DEVICE_SLOT

    /** Saves an invited device's session, in its own slot. */
    fun saveInvited(token: String) = saveSlot(token, DEVICE_SLOT)

    /**
     * Saves [token] as the active session: for household profile [profileId], or
     * the unnamed slot for a password or automatic sign-in. The slot it replaces
     * is dropped.
     */
    fun save(token: String, profileId: Long? = null) = saveSlot(token, profileId?.let { "$PROFILE_PREFIX$it" } ?: "")

    @Synchronized
    private fun saveSlot(token: String, slot: String) {
        val previous = activeSlot()
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val ciphertext = cipher.doFinal(token.toByteArray(Charsets.UTF_8))
        val edit = prefs.edit()
        if (previous != slot) edit.remove(ivKey(previous)).remove(tokenKey(previous))
        edit.putString(ivKey(slot), Base64.encodeToString(cipher.iv, Base64.NO_WRAP))
            .putString(tokenKey(slot), Base64.encodeToString(ciphertext, Base64.NO_WRAP))
            .putString(KEY_ACTIVE, slot)
            .commit()
        cached = token
        loaded = true
        onSessionChange?.invoke()
    }

    /** Signs out: drops the active slot. */
    @Synchronized
    fun clear() {
        val slot = activeSlot()
        prefs.edit().remove(ivKey(slot)).remove(tokenKey(slot)).remove(KEY_ACTIVE).commit()
        cached = null
        loaded = true
        onSessionChange?.invoke()
    }

    private fun activeSlot(): String = prefs.getString(KEY_ACTIVE, null) ?: ""

    private fun decrypt(slot: String): String? {
        val iv = prefs.getString(ivKey(slot), null) ?: return null
        val data = prefs.getString(tokenKey(slot), null) ?: return null
        return try {
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(TAG_BITS, Base64.decode(iv, Base64.NO_WRAP)))
            String(cipher.doFinal(Base64.decode(data, Base64.NO_WRAP)), Charsets.UTF_8)
        } catch (e: Exception) {
            // A lost or replaced keystore key makes the stored token unreadable; sign in again.
            Log.w(TAG, "Stored token unreadable, clearing it", e)
            prefs.edit().remove(ivKey(slot)).remove(tokenKey(slot)).commit()
            null
        }
    }

    private fun key(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getEntry(KEY_ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private companion object {
        const val TAG = "TokenStore"
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "token_key"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val TAG_BITS = 128
        const val KEY_IV = "iv"
        const val KEY_TOKEN = "token"
        const val KEY_ACTIVE = "active_slot"
        const val PROFILE_PREFIX = "profile:"
        const val DEVICE_SLOT = "device"

        // The unnamed slot keeps the original key names, so an existing sign-in survives the update.
        fun ivKey(slot: String) = if (slot.isEmpty()) KEY_IV else "$KEY_IV.$slot"

        fun tokenKey(slot: String) = if (slot.isEmpty()) KEY_TOKEN else "$KEY_TOKEN.$slot"
    }
}
