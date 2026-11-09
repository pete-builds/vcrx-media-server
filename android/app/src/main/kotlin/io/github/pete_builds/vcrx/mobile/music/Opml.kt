package io.github.pete_builds.vcrx.mobile.music

import io.github.pete_builds.vcrx.mobile.auto.Features
import io.github.pete_builds.vcrx.mobile.net.OpmlImportResult
import io.github.pete_builds.vcrx.mobile.net.User
import java.io.InputStream

/**
 * Moving podcast subscriptions in and out as OPML, the file every podcast app
 * reads and writes. The phone sends the file as it is to POST
 * /api/v1/podcasts/opml, which reads it, and saves GET's answer where the
 * listener chooses. Accounts and household profiles only, as for every
 * podcast route: an invited device never gets podcasts. Pure, so it is unit
 * tested.
 */
object Opml {
    /** What the import is sent as; the server also takes application/xml and text/xml. */
    const val MEDIA_TYPE = "text/x-opml"

    /** The server reads at most 1 MB (and 100 feeds) from one file. */
    const val MAX_BYTES = 1024 * 1024

    /** The export's suggested file name. */
    const val EXPORT_NAME = "vcrx-podcasts.opml"

    /** What the file picker offers: OPML has no one agreed type, so any file, checked by [check]. */
    val PICKER_TYPES = arrayOf("text/x-opml", "text/xml", "application/xml", "application/octet-stream", "text/plain", "*/*")

    /** Whether this session may import and export: the podcasts feature, which never includes an invited device. */
    fun allowed(me: User?, invitedDevice: Boolean): Boolean = Features.of(emptyList(), me, invitedDevice).podcasts

    /** Reads at most one byte past [MAX_BYTES], so a huge file is refused without reading it all. */
    fun read(input: InputStream): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        val buf = ByteArray(8192)
        while (out.size() <= MAX_BYTES) {
            val n = input.read(buf, 0, minOf(buf.size, MAX_BYTES + 1 - out.size()))
            if (n < 0) break
            out.write(buf, 0, n)
        }
        return out.toByteArray()
    }

    /** Null when [file] can be sent, else why not, in words for the listener. */
    fun check(file: ByteArray): String? {
        if (file.isEmpty()) return "That file is empty."
        if (file.size > MAX_BYTES) return "That file is over 1 MB. An OPML file of podcasts is far smaller; check it is the right one."
        val head = String(file, 0, minOf(file.size, 4096), Charsets.UTF_8).trimStart('﻿').lowercase()
        if ("<opml" !in head) return "That is not an OPML file. Export one from your podcast app's settings."
        return null
    }

    /** "Added 3 shows. 1 was already in your podcasts. 2 could not be added." */
    fun summary(r: OpmlImportResult): String = buildList {
        add(if (r.added == 1) "Added 1 show." else "Added ${r.added} shows.")
        if (r.already > 0) add(if (r.already == 1) "1 was already in your podcasts." else "${r.already} were already in your podcasts.")
        if (r.failed.isNotEmpty()) add(if (r.failed.size == 1) "1 could not be added." else "${r.failed.size} could not be added.")
    }.joinToString(" ")
}
