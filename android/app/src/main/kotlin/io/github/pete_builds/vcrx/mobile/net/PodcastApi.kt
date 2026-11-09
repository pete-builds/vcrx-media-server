package io.github.pete_builds.vcrx.mobile.net

import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

// The podcast routes, as extensions so the podcast screens live in their own
// files, as MusicApi does. Podcasts answer accounts and household profiles
// only: an invited device gets 403, so the app never shows them there.
// Ported from the TV's net/PodcastApi.kt, plus what only the phone does:
// subscribe, search, new episodes, unsubscribe and refresh.

suspend fun ServerApi.podcasts(): List<Podcast> = get("/api/v1/podcasts", PodcastsResponse.serializer()).podcasts

suspend fun ServerApi.podcastShow(podcastId: Long, limit: Int = 200, offset: Int = 0): PodcastShowResponse =
    get("/api/v1/podcasts/shows/$podcastId?limit=$limit&offset=$offset", PodcastShowResponse.serializer())

suspend fun ServerApi.podcastEpisode(episodeId: Long): PodcastEpisode =
    get("/api/v1/podcasts/episodes/$episodeId", PodcastEpisodeResponse.serializer()).episode

/** New episodes across every subscription, newest first. */
suspend fun ServerApi.podcastNewEpisodes(limit: Int = 50): List<PodcastEpisode> =
    get("/api/v1/podcasts/new?limit=$limit", PodcastEpisodesResponse.serializer()).episodes

/** Subscribes by feed URL; the server fetches the feed before it answers. */
suspend fun ServerApi.podcastSubscribe(feedUrl: String): Podcast =
    send(post("/api/v1/podcasts", PodcastSubscribeRequest(feedUrl), PodcastSubscribeRequest.serializer()), PodcastResult.serializer()).podcast

/** Searches the podcast directory through the server; only when GET /me says podcast_search. */
suspend fun ServerApi.podcastSearch(query: String): List<PodcastSearchResult> {
    val u = url("/api/v1/podcasts/search").newBuilder().addQueryParameter("q", query).build()
    return send(Request.Builder().url(u).build(), PodcastSearchResponse.serializer()).results
}

suspend fun ServerApi.podcastUnsubscribe(podcastId: Long) {
    send<Unit>(Request.Builder().url(url("/api/v1/podcasts/shows/$podcastId")).delete().build(), null)
}

/** Reads the show's feed again now. */
suspend fun ServerApi.podcastRefresh(podcastId: Long): Podcast =
    send(Request.Builder().url(url("/api/v1/podcasts/shows/$podcastId/refresh")).post(ByteArray(0).toRequestBody(null)).build(), PodcastResult.serializer()).podcast

suspend fun ServerApi.podcastSetProgress(episodeId: Long, request: PodcastProgressRequest): PodcastProgress =
    send(put("/api/v1/podcasts/episodes/$episodeId/progress", request, PodcastProgressRequest.serializer()), PodcastProgress.serializer())

suspend fun ServerApi.podcastSetPlayed(episodeId: Long, played: Boolean): PodcastProgress =
    send(put("/api/v1/podcasts/episodes/$episodeId/played", PodcastPlayedRequest(played), PodcastPlayedRequest.serializer()), PodcastProgress.serializer())

fun ServerApi.podcastArtworkUrl(podcastId: Long): String? = absoluteUrl("/api/v1/podcasts/shows/$podcastId/artwork")

fun ServerApi.episodeArtworkUrl(episodeId: Long): String? = absoluteUrl("/api/v1/podcasts/episodes/$episodeId/artwork")
