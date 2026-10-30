#!/usr/bin/env bash
# Builds the Media3 FFmpeg extension's JNI library for each ABI against the static
# FFmpeg libraries made by upstream build_ffmpeg.sh, then stages everything the
# :ffmpeg Gradle module compiles: Java sources, native libraries, ProGuard rules
# and licence texts. Runs inside the ffmpeg stage of tv/Dockerfile.
#
# usage: stage-extension.sh <media3 checkout> <ndk dir> <cmake bin dir> <min sdk> <out dir> <abi>...
set -euo pipefail

media3="$1"
ndk="$2"
cmake_bin="$3"
min_sdk="$4"
out="$5"
shift 5

module="$media3/libraries/decoder_ffmpeg"
main="$module/src/main"
strip="$ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-strip"
readelf="$ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"

rm -rf "$out"
mkdir -p "$out/jniLibs" "$out/licenses"

for abi in "$@"; do
  build="/tmp/jni-build/$abi"
  "$cmake_bin/cmake" -S "$main/jni" -B "$build" -G Ninja \
    -DCMAKE_MAKE_PROGRAM="$cmake_bin/ninja" \
    -DCMAKE_TOOLCHAIN_FILE="$ndk/build/cmake/android.toolchain.cmake" \
    -DANDROID_ABI="$abi" \
    -DANDROID_PLATFORM="android-$min_sdk" \
    -DCMAKE_BUILD_TYPE=Release > /dev/null
  "$cmake_bin/cmake" --build "$build" > /dev/null
  mkdir -p "$out/jniLibs/$abi"
  "$strip" --strip-unneeded -o "$out/jniLibs/$abi/libffmpegJNI.so" "$build/libffmpegJNI.so"
  # The LOAD segment alignment shows whether the library suits 16 KB page devices.
  echo "$abi: $(stat -c %s "$out/jniLibs/$abi/libffmpegJNI.so") bytes, LOAD align $("$readelf" -lW "$out/jniLibs/$abi/libffmpegJNI.so" | awk '/LOAD/ {print $NF; exit}')"
done

cp -r "$main/java" "$out/java"
# DefaultRenderersFactory loads this class by reflection when it is present, and
# upstream marks it as not yet functional, so it stays out of the app.
rm "$out/java/androidx/media3/decoder/ffmpeg/ExperimentalFfmpegVideoRenderer.java"
cp "$module/proguard-rules.txt" "$out/proguard-rules.txt"

cp "$media3/LICENSE" "$out/licenses/media3-LICENSE.txt"
cp "$main/jni/ffmpeg/COPYING.LGPLv2.1" "$out/licenses/ffmpeg-COPYING.LGPLv2.1.txt"
{
  echo "media3 $(git -C "$media3" describe --tags --always) $(git -C "$media3" rev-parse HEAD)"
  echo "ffmpeg $(cat "$main/jni/ffmpeg/VERSION" 2> /dev/null || echo unknown)"
  echo "ndk $(sed -n 's/^Pkg.Revision *= *//p' "$ndk/source.properties")"
} > "$out/VERSIONS"
cat "$out/VERSIONS"
find "$out" -type f | sort
