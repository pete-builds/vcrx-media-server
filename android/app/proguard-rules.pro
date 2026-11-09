# Navigation routes and API models are kotlinx.serialization classes. The library
# ships equivalent rules; these keep the app working if they ever change.
-keep,includedescriptorclasses class io.github.pete_builds.vcrx.mobile.**$$serializer { *; }
-keepclassmembers class io.github.pete_builds.vcrx.mobile.** {
    *** Companion;
}
-keepclasseswithmembers class io.github.pete_builds.vcrx.mobile.** {
    kotlinx.serialization.KSerializer serializer(...);
}
