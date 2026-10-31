package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.settings.Languages
import io.github.pete_builds.vcrx.tv.settings.SubtitleMode
import io.github.pete_builds.vcrx.tv.settings.SubtitlePrefs
import kotlinx.serialization.Serializable

/**
 * One subtitle track a title offers, from the player's track list (direct play)
 * or the session's WebVTT list (converted). [id] finds it again in the same playback.
 */
data class SubtitleCandidate(
    val id: String,
    val language: String?,
    val forced: Boolean = false,
    val isDefault: Boolean = false,
    val label: String? = null,
    val supported: Boolean = true,
)

/**
 * A choice the viewer made during playback, by what the track is rather than
 * where it sits, so it can be found again in the next episode's file.
 */
@Serializable
data class StickyChoice(
    val on: Boolean,
    val language: String? = null,
    val forced: Boolean = false,
    val label: String? = null,
)

/**
 * Which subtitle track a title opens with. Pure, so every mode and language case
 * is unit tested. A null answer means subtitles off.
 *
 * Order of authority: what the viewer chose during playback for this title or
 * its show ([StickyChoice]), then Settings. Settings Off means no track at all,
 * forced ones included; changing it in Settings clears the sticky choices, so
 * the switch always means what it says.
 */
object SubtitlePicker {
    fun choose(
        prefs: SubtitlePrefs,
        candidates: List<SubtitleCandidate>,
        audioLanguage: String?,
        sticky: StickyChoice?,
        deviceLanguage: String,
    ): SubtitleCandidate? {
        val usable = candidates.filter { it.supported }
        if (sticky != null) {
            if (!sticky.on) return null
            // The chosen language is missing from this file: Settings decide instead.
            match(usable, sticky)?.let { return it }
        }
        if (!prefs.enabled) return null
        val languages = Languages.preferred(prefs.languages, deviceLanguage)
        return when (prefs.mode) {
            SubtitleMode.Always -> full(usable, languages) ?: forced(usable, languages) ?: untaggedDefault(usable)
            SubtitleMode.ForcedOnly -> forced(usable, languages)
            SubtitleMode.ForeignAudio ->
                if (audioIsMine(audioLanguage, languages)) forced(usable, languages)
                else full(usable, languages) ?: forced(usable, languages)
        }
    }

    /**
     * The track one press of the subtitle toggle turns on: a full track in a
     * preferred language, else a forced one, else the first full track of any
     * language. The viewer asked for subtitles, so something shows if anything can.
     */
    fun forToggleOn(prefs: SubtitlePrefs, candidates: List<SubtitleCandidate>, deviceLanguage: String): SubtitleCandidate? {
        val usable = candidates.filter { it.supported }
        val languages = Languages.preferred(prefs.languages, deviceLanguage)
        return full(usable, languages) ?: forced(usable, languages)
            ?: usable.firstOrNull { !it.forced && it.isDefault } ?: usable.firstOrNull { !it.forced } ?: usable.firstOrNull()
    }

    /** What to remember when the viewer picks [chosen] (null: turned off). */
    fun sticky(chosen: SubtitleCandidate?): StickyChoice =
        if (chosen == null) StickyChoice(on = false)
        else StickyChoice(on = true, language = Languages.base(chosen.language), forced = chosen.forced, label = chosen.label)

    /**
     * Audio in a preferred language, or untagged, counts as the viewer's own: most
     * untagged tracks in a library are in its owner's language, and guessing
     * otherwise would put subtitles over every untagged film.
     */
    fun audioIsMine(audioLanguage: String?, languages: List<String>): Boolean {
        val audio = Languages.base(audioLanguage) ?: return true
        return audio in languages
    }

    private fun full(usable: List<SubtitleCandidate>, languages: List<String>): SubtitleCandidate? =
        firstInLanguages(usable.filter { !it.forced }, languages)

    private fun forced(usable: List<SubtitleCandidate>, languages: List<String>): SubtitleCandidate? =
        firstInLanguages(usable.filter { it.forced }, languages)

    /** The first language in preference order that has a track wins; within it, the file's default track. */
    private fun firstInLanguages(tracks: List<SubtitleCandidate>, languages: List<String>): SubtitleCandidate? {
        for (language in languages) {
            val matching = tracks.filter { Languages.base(it.language) == language }
            if (matching.isNotEmpty()) return matching.firstOrNull { it.isDefault } ?: matching.first()
        }
        return null
    }

    /** Always mode in a file whose tracks carry no language: the one the file marks default, if any. */
    private fun untaggedDefault(usable: List<SubtitleCandidate>): SubtitleCandidate? =
        usable.firstOrNull { it.isDefault && !it.forced && Languages.base(it.language) == null }

    private fun match(usable: List<SubtitleCandidate>, sticky: StickyChoice): SubtitleCandidate? {
        val language = Languages.base(sticky.language)
        val same = usable.filter { Languages.base(it.language) == language && it.forced == sticky.forced }
        return same.firstOrNull { sticky.label != null && it.label == sticky.label } ?: same.firstOrNull()
    }
}

/**
 * Choices made during playback, per title and per show, newest last and capped so
 * the store cannot grow without bound. A title's own choice outranks its show's,
 * so going back to an earlier episode keeps what was chosen there.
 */
class SubtitleMemory(initial: Map<String, StickyChoice> = emptyMap(), private val max: Int = MAX_ENTRIES) {
    private val entries = LinkedHashMap(initial)

    fun lookup(itemId: Long, showId: Long?): StickyChoice? =
        entries[itemKey(itemId)] ?: showId?.let { entries[showKey(it)] }

    fun remember(itemId: Long, showId: Long?, choice: StickyChoice) {
        put(itemKey(itemId), choice)
        if (showId != null) put(showKey(showId), choice)
    }

    fun clear() = entries.clear()

    val size: Int get() = entries.size

    fun snapshot(): Map<String, StickyChoice> = LinkedHashMap(entries)

    private fun put(key: String, choice: StickyChoice) {
        entries.remove(key)
        entries[key] = choice
        while (entries.size > max) entries.remove(entries.keys.first())
    }

    private companion object {
        const val MAX_ENTRIES = 400
        fun itemKey(id: Long) = "item:$id"
        fun showKey(id: Long) = "show:$id"
    }
}
