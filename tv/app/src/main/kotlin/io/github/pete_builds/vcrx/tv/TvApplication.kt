package io.github.pete_builds.vcrx.tv

import android.app.Application
import android.content.Context
import coil3.ImageLoader
import coil3.PlatformContext
import coil3.SingletonImageLoader
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import coil3.request.crossfade

class TvApplication : Application(), SingletonImageLoader.Factory {
    lateinit var graph: AppGraph
        private set

    override fun onCreate() {
        super.onCreate()
        graph = AppGraph(this)
        if (graph.tokens.token() != null) graph.playbackEvents.flushPending()
    }

    /** Coil fetches artwork through the shared client, so posters carry the bearer token too. */
    override fun newImageLoader(context: PlatformContext): ImageLoader =
        ImageLoader.Builder(context)
            .components { add(OkHttpNetworkFetcherFactory(callFactory = { graph.httpClient })) }
            .crossfade(true)
            .build()
}

val Context.graph: AppGraph
    get() = (applicationContext as TvApplication).graph
