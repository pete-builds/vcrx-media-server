package io.github.pete_builds.vcrx.tv.settings

import java.util.Locale

/**
 * Language codes as files tag them ("eng", "fre", "en", "pt-BR") reduced to one
 * comparable form, the two-letter code, so a preference for "en" matches a track
 * tagged "eng". Pure, so the matching every subtitle and audio choice rests on is
 * unit tested.
 */
object Languages {
    /** ISO 639-2 bibliographic codes, which Locale does not know; the terminology ones it does. */
    private val BIBLIOGRAPHIC = mapOf(
        "alb" to "sq", "arm" to "hy", "baq" to "eu", "bur" to "my", "chi" to "zh", "cze" to "cs",
        "dut" to "nl", "fre" to "fr", "geo" to "ka", "ger" to "de", "gre" to "el", "ice" to "is",
        "mac" to "mk", "mao" to "mi", "may" to "ms", "per" to "fa", "rum" to "ro", "slo" to "sk",
        "tib" to "bo", "wel" to "cy",
    )

    /** Codes that name no language: undetermined, multiple, none. */
    private val NONE = setOf("", "und", "mul", "zxx", "mis", "unk")

    private val threeToTwo: Map<String, String> by lazy {
        Locale.getISOLanguages().mapNotNull { two ->
            runCatching { Locale.forLanguageTag(two).isO3Language }.getOrNull()?.takeIf { it.isNotEmpty() }?.let { it to two }
        }.toMap()
    }

    /** The two-letter code for [code], or null when it names no language. Unknown codes pass through lowercased. */
    fun base(code: String?): String? {
        val lower = code?.trim()?.lowercase()?.substringBefore('-')?.substringBefore('_') ?: return null
        if (lower in NONE) return null
        if (lower.length == 3) return BIBLIOGRAPHIC[lower] ?: threeToTwo[lower] ?: lower
        return lower
    }

    /** True when both name the same language. Two untagged tracks are not the same language. */
    fun same(a: String?, b: String?): Boolean {
        val x = base(a) ?: return false
        return x == base(b)
    }

    /** "en" to "English"; the code itself when Locale has no name for it. */
    fun name(code: String): String {
        val two = base(code) ?: return code
        val name = Locale.forLanguageTag(two).getDisplayLanguage(Locale.ENGLISH)
        return if (name.isNullOrBlank() || name.equals(two, ignoreCase = true)) code else name
    }

    /** The preferred list with the device language standing in for an empty one. */
    fun preferred(codes: List<String>, deviceLanguage: String): List<String> {
        val list = codes.mapNotNull { base(it) }.distinct()
        return list.ifEmpty { listOfNotNull(base(deviceLanguage)) }
    }

    /** Offered in the language pickers, most widely spoken first after the device's own. */
    val COMMON = listOf(
        "en", "es", "fr", "de", "it", "pt", "nl", "sv", "no", "da", "fi", "pl", "cs", "hu", "el", "tr",
        "ru", "uk", "ar", "he", "hi", "ja", "ko", "zh", "th", "vi", "id",
    )
}
