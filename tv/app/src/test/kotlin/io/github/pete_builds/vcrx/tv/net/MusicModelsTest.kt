package io.github.pete_builds.vcrx.tv.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Music payloads as the server sends them (copied from a dev server), nulls and unknown fields included. */
class MusicModelsTest {
    private val json = ApiJson

    @Test
    fun liveAlbumWithItsTracks() {
        val body = """
            {"id":4,"library_id":3,"artist_id":4,"artist_name":"Glass Harbor","title":"2024-06-14 The Lantern Room, Portland, OR",
             "year":2024,"date":"2024-06-14","genre":null,"compilation":false,"live":true,"venue":"The Lantern Room",
             "location":"Portland, OR","source":"SBD","track_count":3,"duration_ms":15000,"has_cover":false,"added_at":1,
             "source_detail":"SBD > Zoom F8 > SD","lineage":"SD > WAV > FLAC","taper":"Mo Ellery","info_text":"Glass Harbor",
             "musicbrainz_id":null,"replaygain":{"track_gain":null,"album_gain":null},
             "tracks":[{"id":7,"library_id":3,"album_id":4,"artist_id":4,"title":"Low Tide","artist_name":"Glass Harbor",
               "album_title":"2024-06-14 The Lantern Room, Portland, OR","album_artist":"Glass Harbor","disc_number":1,
               "track_number":1,"duration_ms":5000,"codec":"flac","container":"flac","bit_depth":24,
               "replaygain":{"track_gain":-3.1,"track_peak":null,"album_gain":null,"album_peak":null},"has_cover":false,
               "md5":"f32d6d0d69171737ac372ccc8680cc63","ffp":"f53ecf67837490ad91fbcd7a2750bccb"}],
             "checksums":[{"file_name":"a.flac","algorithm":"ffp","value":"00","source":"a.ffp","track_id":7}]}
        """.trimIndent()
        val a = json.decodeFromString(MusicAlbumDetail.serializer(), body)
        assertTrue(a.live)
        assertEquals("SBD > Zoom F8 > SD", a.sourceDetail)
        assertNull(a.genre)
        assertEquals(1, a.tracks.size)
        assertEquals(-3.1, a.tracks[0].replaygain.trackGain!!, 0.0)
        assertEquals(1, a.tracks[0].trackNumber)
        assertNull(a.tracks[0].stream)
    }

    @Test
    fun queueWithAConvertedAndADirectTrack() {
        val body = """
            {"tracks":[
              {"id":1,"title":"Apogee","album_id":1,"artist_id":1,"artist_name":"Cedar Static","album_title":"Low Orbit",
               "disc_number":null,"track_number":1,"duration_ms":10000,"codec":"alac","has_cover":false,
               "replaygain":{"track_gain":null},"md5":null,
               "stream":{"url":"/api/v1/music/tracks/1/stream?codec=aac","protocol":"converted","mime_type":"audio/aac",
                 "decision":"audio_transcode","reason":"this device does not decode alac; converting to AAC"},"refusal":null},
              {"id":7,"title":"Low Tide","stream":{"url":"/api/v1/music/tracks/7/file","protocol":"progressive",
                 "mime_type":"audio/flac","decision":"direct_play","reason":"ok"},"refusal":null},
              {"id":9,"title":"Undertow","stream":null,"refusal":{"code":"playback_refused","message":"Not for your account."}}]}
        """.trimIndent()
        val q = json.decodeFromString(MusicQueueResponse.serializer(), body).tracks
        assertEquals(3, q.size)
        assertFalse(q[0].stream!!.seekable)
        assertTrue(q[1].stream!!.seekable)
        assertNull(q[2].stream)
        assertEquals("playback_refused", q[2].refusal!!.code)
    }

    @Test
    fun musicCapabilitiesAddPcmAndAudioContainers() {
        val video = Capabilities(
            containers = listOf("mkv", "mp4"),
            videoCodecs = emptyList(),
            audioCodecs = listOf(AudioCodecCapability("aac"), AudioCodecCapability("flac"), AudioCodecCapability("pcm", 2)),
        )
        val music = musicCapabilities(video)
        assertTrue("flac" in music.containers && "wav" in music.containers && "m4a" in music.containers)
        assertFalse("shn" in music.containers)
        assertEquals(listOf("aac", "flac", "pcm"), music.audioCodecs.map { it.codec })
        // A request carries them under the names the server reads.
        val encoded = json.encodeToString(MusicQueueRequest.serializer(), MusicQueueRequest(albumId = 4, capabilities = music))
        assertTrue(encoded.contains("\"album_id\":4"))
        assertFalse(encoded.contains("artist_id"))
        assertTrue(encoded.contains("\"audio_codecs\""))
    }

    @Test
    fun albumLine() {
        val show = MusicAlbum(id = 1, title = "x", artistName = "Glass Harbor", live = true, date = "2024-06-14", venue = "The Lantern Room", year = 2024)
        assertEquals("Glass Harbor · 2024-06-14 · The Lantern Room", show.line())
        val studio = MusicAlbum(id = 2, title = "Signal Fires", artistName = "Glass Harbor", year = 2019)
        assertEquals("2019", studio.line(withArtist = false))
    }
}
