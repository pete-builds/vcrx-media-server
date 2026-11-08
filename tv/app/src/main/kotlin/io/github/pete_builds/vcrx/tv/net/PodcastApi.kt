package io.github.pete_builds.vcrx.tv.net

// The podcast routes, as extensions so the podcast screens live in their own
// files, as MusicApi does. The TV lists and plays what the user subscribed to
// elsewhere; subscribing by URL with a remote is not offered.

suspend fun ServerApi.podcasts(): List<Podcast> = get("/api/v1/podcasts", PodcastsResponse.serializer()).podcasts

suspend fun ServerApi.podcastShow(podcastId: Long, limit: Int = 100, offset: Int = 0): PodcastShowResponse =
    get("/api/v1/podcasts/shows/$podcastId?limit=$limit&offset=$offset", PodcastShowResponse.serializer())

suspend fun ServerApi.podcastSetProgress(episodeId: Long, request: PodcastProgressRequest): PodcastProgress =
    send(put("/api/v1/podcasts/episodes/$episodeId/progress", request, PodcastProgressRequest.serializer()), PodcastProgress.serializer())

suspend fun ServerApi.podcastSetPlayed(episodeId: Long, played: Boolean): PodcastProgress =
    send(put("/api/v1/podcasts/episodes/$episodeId/played", PodcastPlayedRequest(played), PodcastPlayedRequest.serializer()), PodcastProgress.serializer())

fun ServerApi.podcastArtworkUrl(podcastId: Long): String? = absoluteUrl("/api/v1/podcasts/shows/$podcastId/artwork")

fun ServerApi.episodeArtworkUrl(episodeId: Long): String? = absoluteUrl("/api/v1/podcasts/episodes/$episodeId/artwork")
