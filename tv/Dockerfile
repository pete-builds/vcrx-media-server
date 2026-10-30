# Reproducible build of the VCRX TV APK. Every toolchain input is pinned here, so
# a clean checkout builds the same app on any Docker host (see tv/README.md).
#
# Stages:
#   sdk       JDK, Android cmdline-tools, platform and build-tools
#   ffmpeg    Media3 FFmpeg audio extension built from source (vanilla FFmpeg, LGPL)
#   deps      Gradle distribution and dependencies, cached until build files change
#   check     lint, unit tests and the debug APK (what CI runs)
#   release   unit tests, then debug and release APKs signed with the local keystore
#   apk       scratch stage holding only the APKs, for --output type=local
#   ffmpeg-ext  scratch stage holding the staged extension, for builds outside Docker

ARG JDK_IMAGE=eclipse-temurin:21.0.13_9-jdk-noble@sha256:088afe113afcf30db6e0919cca3cded8666e10b90c876c4a317ee4318f532f4b

FROM ${JDK_IMAGE} AS sdk
# commandlinetools build and SHA-256 as published on developer.android.com/studio.
ARG CMDLINE_TOOLS_BUILD=15859902
ARG CMDLINE_TOOLS_SHA256=4e4c464f145a7512b57d088ac6c278c03c9eea610886b35a5e0804e74eedf583
ARG SDK_PLATFORM=android-37.0
ARG BUILD_TOOLS=36.0.0
ENV ANDROID_HOME=/opt/android-sdk \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    GRADLE_USER_HOME=/opt/gradle-home
ENV PATH=${ANDROID_HOME}/cmdline-tools/latest/bin:${PATH}
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip \
 && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    curl -fsSLo /tmp/clt.zip "https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS_BUILD}_latest.zip"; \
    echo "${CMDLINE_TOOLS_SHA256}  /tmp/clt.zip" | sha256sum -c -; \
    mkdir -p "${ANDROID_HOME}/cmdline-tools"; \
    unzip -q /tmp/clt.zip -d /tmp/clt; \
    mv /tmp/clt/cmdline-tools "${ANDROID_HOME}/cmdline-tools/latest"; \
    rm -rf /tmp/clt /tmp/clt.zip
# sdkmanager verifies each package against the checksum in Google's repository index.
RUN yes | sdkmanager --licenses > /dev/null \
 && sdkmanager --install "platforms;${SDK_PLATFORM}" "build-tools;${BUILD_TOOLS}" \
 && sdkmanager --list_installed

FROM sdk AS ffmpeg
# NDK r28c is AGP 9.5's default and links with 16 KB pages by default.
ARG NDK_VERSION=28.2.13676358
ARG CMAKE_VERSION=3.31.6
ARG MEDIA3_TAG=1.11.1
ARG MEDIA3_COMMIT=8c6678b657ede1e7883fc164ef73ed483c7796c3
# The Media3 1.11.1 FFmpeg README recommends FFmpeg 6.0; 6.0.1 is its latest point
# release. Checksum taken after verifying the release signature (key FCF986EA15E6E293).
ARG FFMPEG_VERSION=6.0.1
ARG FFMPEG_SHA256=9b16b8731d78e596b4be0d720428ca42df642bb2d78342881ff7f5bc29fc9623
# Must equal minSdk in app/build.gradle.kts.
ARG MIN_SDK=26
ARG ENABLED_DECODERS="aac ac3 eac3 dca mlp truehd flac alac opus vorbis mp3 pcm_mulaw pcm_alaw"
RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc git libc6-dev make xz-utils \
 && rm -rf /var/lib/apt/lists/*
RUN sdkmanager --install "ndk;${NDK_VERSION}" "cmake;${CMAKE_VERSION}"
RUN set -eu; \
    git clone --quiet --depth 1 --branch "${MEDIA3_TAG}" --filter=blob:none --sparse \
      https://github.com/androidx/media.git /src/media; \
    cd /src/media; \
    git sparse-checkout set libraries/decoder_ffmpeg; \
    test "$(git rev-parse HEAD)" = "${MEDIA3_COMMIT}"
RUN set -eu; \
    curl -fsSLo /src/ffmpeg.tar.xz "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"; \
    echo "${FFMPEG_SHA256}  /src/ffmpeg.tar.xz" | sha256sum -c -; \
    mkdir -p /src/media/libraries/decoder_ffmpeg/src/main/jni/ffmpeg; \
    tar -xJf /src/ffmpeg.tar.xz --strip-components=1 -C /src/media/libraries/decoder_ffmpeg/src/main/jni/ffmpeg; \
    rm /src/ffmpeg.tar.xz
# Upstream build_ffmpeg.sh, unmodified. It builds all four ABIs; the app ships three.
# Runs at low priority so a build on a shared host does not starve other services.
RUN set -eu; \
    module=/src/media/libraries/decoder_ffmpeg/src/main; \
    cd "$module/jni"; \
    nice -n 19 ./build_ffmpeg.sh "$module" "${ANDROID_HOME}/ndk/${NDK_VERSION}" linux-x86_64 "${MIN_SDK}" ${ENABLED_DECODERS} > /tmp/build_ffmpeg.log 2>&1 \
      || { tail -n 80 /tmp/build_ffmpeg.log; exit 1; }; \
    tail -n 5 /tmp/build_ffmpeg.log; \
    grep -q '#define FFMPEG_LICENSE "LGPL version 2.1 or later"' ffmpeg/config.h; \
    grep FFMPEG_LICENSE ffmpeg/config.h; \
    ls -la ffmpeg/android-libs/*
COPY ffmpeg/stage-extension.sh /usr/local/bin/stage-extension.sh
RUN nice -n 19 stage-extension.sh /src/media "${ANDROID_HOME}/ndk/${NDK_VERSION}" \
      "${ANDROID_HOME}/cmake/${CMAKE_VERSION}/bin" "${MIN_SDK}" /out/ext armeabi-v7a arm64-v8a x86_64

FROM scratch AS ffmpeg-ext
COPY --from=ffmpeg /out/ext /

FROM sdk AS deps
WORKDIR /src
COPY gradlew ./
COPY gradle/ gradle/
RUN ./gradlew --no-daemon --version
COPY settings.gradle.kts build.gradle.kts gradle.properties ./
COPY app/build.gradle.kts app/proguard-rules.pro app/
COPY ffmpeg/build.gradle.kts ffmpeg/
# Resolving every configuration downloads the plugins and libraries into this layer.
RUN ./gradlew --no-daemon --quiet :app:dependencies :ffmpeg:dependencies > /dev/null

FROM deps AS source
COPY --from=ffmpeg /out/ext ffmpeg/ext
COPY . .

FROM source AS check
RUN nice -n 19 ./gradlew --no-daemon :app:lintDebug :app:testDebugUnitTest :app:assembleDebug

FROM source AS release
ARG VERSION_CODE=1
# Hash of the keystore file, so a new key never reuses an APK signed with the old one.
ARG KEYSTORE_ID=none
RUN --mount=type=secret,id=release_keystore,required=true \
    --mount=type=secret,id=release_keystore_pass,required=true \
    set -eu; \
    RELEASE_KEYSTORE=/run/secrets/release_keystore \
    RELEASE_KEYSTORE_PASS_FILE=/run/secrets/release_keystore_pass \
    nice -n 19 ./gradlew --no-daemon -PappVersionCode="${VERSION_CODE}" \
      :app:testDebugUnitTest :app:assembleDebug :app:assembleRelease; \
    mkdir -p /out; \
    cp app/build/outputs/apk/release/app-release.apk /out/vcrx-tv-release.apk; \
    cp app/build/outputs/apk/debug/app-debug.apk /out/vcrx-tv-debug.apk; \
    aapt2="$(ls "${ANDROID_HOME}"/build-tools/*/aapt2 | head -n 1)"; \
    "$aapt2" dump badging /out/vcrx-tv-release.apk \
      | grep -E "^(package|application-label|launchable-activity|leanback-launchable-activity|native-code|sdkVersion|targetSdkVersion):"; \
    ls -la /out

FROM scratch AS apk
COPY --from=release /out /
