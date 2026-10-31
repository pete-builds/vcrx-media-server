package io.github.pete_builds.vcrx.tv.net

import okhttp3.Request

// The music routes, as extensions so the music screens live in their own files.
// Same transport, errors and 401 handling as every other call in ServerApi.

suspend fun ServerApi.musicArtists(libraryId: Long, limit: Int = 200, offset: Int = 0): MusicArtistPage =
    get("/api/v1/music/libraries/$libraryId/artists?limit=$limit&offset=$offset", MusicArtistPage.serializer())

/** [live] true lists only live shows, false only the rest, null both. */
suspend fun ServerApi.musicAlbums(libraryId: Long, sort: String, live: Boolean?, limit: Int = 200, offset: Int = 0): MusicAlbumPage {
    val url = url("/api/v1/music/libraries/$libraryId/albums").newBuilder()
        .addQueryParameter("sort", sort)
        .addQueryParameter("limit", limit.toString())
        .addQueryParameter("offset", offset.toString())
        .apply { if (live != null) addQueryParameter("live", live.toString()) }
        .build()
    return send(Request.Builder().url(url).build(), MusicAlbumPage.serializer())
}

suspend fun ServerApi.musicArtist(artistId: Long): MusicArtistDetail =
    get("/api/v1/music/artists/$artistId", MusicArtistDetail.serializer())

suspend fun ServerApi.musicAlbum(albumId: Long): MusicAlbumDetail =
    get("/api/v1/music/albums/$albumId", MusicAlbumDetail.serializer())

suspend fun ServerApi.musicQueue(request: MusicQueueRequest): List<MusicTrack> =
    send(post("/api/v1/music/queue", request, MusicQueueRequest.serializer()), MusicQueueResponse.serializer()).tracks

suspend fun ServerApi.musicListen(trackId: Long, capabilities: Capabilities): MusicListen =
    send(post("/api/v1/music/tracks/$trackId/sessions", MusicListenRequest(capabilities), MusicListenRequest.serializer()), MusicListen.serializer())

fun ServerApi.albumCoverUrl(albumId: Long, width: Int): String? = absoluteUrl("/api/v1/music/albums/$albumId/cover?width=$width")

fun ServerApi.artistImageUrl(artistId: Long, width: Int): String? = absoluteUrl("/api/v1/music/artists/$artistId/image?width=$width")
