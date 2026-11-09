# Reproducible build of the VCRX phone and tablet APK. Every toolchain input is
# pinned here, and the sdk stage is byte for byte the TV's (tv/Dockerfile), so the
# two share that cached layer on one Docker host (see android/README.md).
#
# Stages:
#   sdk       JDK, Android cmdline-tools, platform and build-tools
#   deps      Gradle distribution and dependencies, cached until build files change
#   check     lint, unit tests and the debug APK (what CI runs)
#   release   unit tests, then debug and release APKs signed with the local keystore
#   apk       scratch stage holding only the APKs, for --output type=local
#
# No FFmpeg stage: phones decode AAC, MP3, FLAC, Opus and Vorbis themselves, and the
# capabilities the app sends leave out what this phone cannot decode (AC-3, DTS,
# TrueHD on most), so the server converts that audio. Building the TV's extension
# under amd64 emulation would add a long native compile for little gain.

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

FROM sdk AS deps
WORKDIR /src
COPY gradlew ./
COPY gradle/ gradle/
RUN ./gradlew --no-daemon --version
COPY settings.gradle.kts build.gradle.kts gradle.properties ./
COPY app/build.gradle.kts app/proguard-rules.pro app/
# Resolving every configuration downloads the plugins and libraries into this layer.
RUN ./gradlew --no-daemon --quiet :app:dependencies > /dev/null

FROM deps AS source
# The server's API contract for RequestContractTest, at ../server/api as in the repo.
# A named context: CI and build-apk.sh pass --build-context openapi=server/api.
COPY --from=openapi openapi.yaml /server/api/openapi.yaml
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
    cp app/build/outputs/apk/release/app-release.apk /out/vcrx-mobile-release.apk; \
    cp app/build/outputs/apk/debug/app-debug.apk /out/vcrx-mobile-debug.apk; \
    aapt2="$(ls "${ANDROID_HOME}"/build-tools/*/aapt2 | head -n 1)"; \
    "$aapt2" dump badging /out/vcrx-mobile-release.apk \
      | grep -E "^(package|application-label|launchable-activity|sdkVersion|targetSdkVersion):"; \
    ls -la /out

FROM scratch AS apk
COPY --from=release /out /
