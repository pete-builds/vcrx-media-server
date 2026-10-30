# Headless Android TV emulator for tv/scripts/emulator-smoke.sh. Throwaway: the
# script builds it, runs one container with /dev/kvm, and removes the container.
# Pins match tv/Dockerfile; the emulator package is whatever Google marks stable,
# since its repository offers no older stable builds to pin to.
ARG JDK_IMAGE=eclipse-temurin:21.0.13_9-jdk-noble@sha256:088afe113afcf30db6e0919cca3cded8666e10b90c876c4a317ee4318f532f4b
FROM ${JDK_IMAGE}
ARG CMDLINE_TOOLS_BUILD=15859902
ARG CMDLINE_TOOLS_SHA256=4e4c464f145a7512b57d088ac6c278c03c9eea610886b35a5e0804e74eedf583
# AOSP Android TV: no Google Play services, like the app. The only x86_64 TV image.
ARG SYSTEM_IMAGE="system-images;android-36;android-tv;x86_64"
ENV ANDROID_HOME=/opt/android-sdk \
    ANDROID_SDK_ROOT=/opt/android-sdk \
    ANDROID_AVD_HOME=/root/.android/avd
ENV PATH=${ANDROID_HOME}/cmdline-tools/latest/bin:${ANDROID_HOME}/platform-tools:${ANDROID_HOME}/emulator:${PATH}
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip python3 \
      libpulse0 libgl1 libnss3 libxcomposite1 libxcursor1 libxi6 libxtst6 libxdamage1 \
      libxkbfile1 libxrandr2 libasound2t64 libdbus-1-3 libfontconfig1 libx11-6 libxext6 \
      libxfixes3 libxrender1 libxkbcommon0 libdrm2 libgbm1 libxcb-cursor0 \
 && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    curl -fsSLo /tmp/clt.zip "https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS_BUILD}_latest.zip"; \
    echo "${CMDLINE_TOOLS_SHA256}  /tmp/clt.zip" | sha256sum -c -; \
    mkdir -p "${ANDROID_HOME}/cmdline-tools"; \
    unzip -q /tmp/clt.zip -d /tmp/clt; \
    mv /tmp/clt/cmdline-tools "${ANDROID_HOME}/cmdline-tools/latest"; \
    rm -rf /tmp/clt /tmp/clt.zip
RUN yes | sdkmanager --licenses > /dev/null \
 && sdkmanager --install emulator platform-tools "${SYSTEM_IMAGE}" > /dev/null \
 && sdkmanager --list_installed
# A hardware keyboard keeps the on-screen keyboard away, so adb can type into fields.
RUN echo no | avdmanager create avd -n tv -k "${SYSTEM_IMAGE}" -d tv_1080p > /dev/null \
 && printf '%s\n' hw.keyboard=yes hw.dPad=yes hw.ramSize=3072 hw.cpu.ncore=4 disk.dataPartition.size=4G \
      >> "${ANDROID_AVD_HOME}/tv.avd/config.ini"
COPY mock_server.py run.sh /opt/smoke/
RUN chmod 755 /opt/smoke/run.sh
