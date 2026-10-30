#!/usr/bin/env bash
# Runs inside the emulator container: starts the mock server and the emulator,
# installs /tmp/app.apk, drives the app with D-pad key events and saves a
# screenshot after each step to /out. UI steps never abort the run, so a failure
# late in the walk still leaves the earlier screenshots.
set -uo pipefail
out=/out
pkg=io.github.pete_builds.vcrx.tv
mkdir -p "$out"

python3 /opt/smoke/mock_server.py 3750 "$out" > "$out/mock-server.log" 2>&1 &
# The adb server creates ~/.android/adbkey on first start, and the emulator only
# authorizes a key that exists when it boots, so adb must start first.
adb start-server > /dev/null
emulator -avd tv -no-window -no-audio -no-boot-anim -no-snapshot -no-metrics \
  -gpu swiftshader_indirect -accel on > "$out/emulator.log" 2>&1 &

timeout 300 adb wait-for-device || { echo "smoke: emulator never appeared"; tail -40 "$out/emulator.log"; exit 1; }
for _ in $(seq 1 180); do
  [ "$(adb shell getprop sys.boot_completed 2> /dev/null | tr -d '\r')" = "1" ] && break
  sleep 2
done
[ "$(adb shell getprop sys.boot_completed | tr -d '\r')" = "1" ] || { echo "smoke: boot timed out"; exit 1; }
echo "smoke: booted $(adb shell getprop ro.build.fingerprint | tr -d '\r')"
echo "smoke: abis $(adb shell getprop ro.product.cpu.abilist | tr -d '\r')"
for s in window_animation_scale transition_animation_scale animator_duration_scale; do
  adb shell settings put global "$s" 0
done

# The TV keyboard ignores the emulated hardware keyboard and captures the D-pad.
# Disabling its IME is not enough (Android falls back to the default one), so the
# keyboard packages are disabled; adb's typed text then arrives as hardware key
# events, which Compose text fields accept directly.
for ime_pkg in $(adb shell ime list -a -s | tr -d '\r' | cut -d/ -f1 | sort -u); do
  adb shell pm disable-user --user 0 "$ime_pkg" > /dev/null 2>&1 && echo "smoke: disabled keyboard package $ime_pkg"
done

adb install -r /tmp/app.apk || { echo "smoke: install failed"; exit 1; }
adb logcat -c

shot() {
  adb exec-out screencap -p > "$out/$1.png"
  echo "smoke: screenshot $1.png"
}
key() {
  for k in "$@"; do
    adb shell input keyevent "$k"
    sleep 1
  done
}
keyboard_shown() {
  adb shell dumpsys input_method | grep -qE 'mInputShown=true|mIsInputViewShown=true'
}
type_text() {
  adb shell input text "$1"
  sleep 1
  # A user closes the on-screen keyboard with Back before moving on; do the same.
  if keyboard_shown; then
    echo "smoke: keyboard was shown, closing it"
    adb shell input keyevent KEYCODE_BACK
    sleep 1
  fi
}

adb shell am start -W -n "$pkg/.ui.MainActivity" > /dev/null
sleep 6
shot 01-connect
type_text "10.0.2.2:3750"
shot 02-connect-typed
key KEYCODE_DPAD_DOWN KEYCODE_DPAD_CENTER
sleep 5
shot 03-login
type_text "pete"
key KEYCODE_DPAD_DOWN
type_text "smoke-pass"
key KEYCODE_DPAD_DOWN KEYCODE_DPAD_CENTER
sleep 6
shot 04-home
key KEYCODE_DPAD_CENTER
sleep 4
shot 05-details
key KEYCODE_DPAD_CENTER
sleep 8
shot 06-play-refused
key KEYCODE_BACK
sleep 2
key KEYCODE_BACK
sleep 3
shot 07-home-again
key KEYCODE_DPAD_LEFT
sleep 2
shot 08-rail
key KEYCODE_DPAD_DOWN KEYCODE_DPAD_CENTER
sleep 5
shot 09-library
key KEYCODE_BACK KEYCODE_DPAD_DOWN KEYCODE_DPAD_DOWN KEYCODE_DPAD_DOWN KEYCODE_DPAD_CENTER
sleep 3
shot 10-settings
key KEYCODE_DPAD_DOWN KEYCODE_DPAD_DOWN KEYCODE_DPAD_CENTER
sleep 6
shot 11-about

adb logcat -d -b crash > "$out/crash.log"
# Only this app's lines and runtime errors; the rest of the system log is noise.
adb logcat -d | grep -E "$pkg|AndroidRuntime|FATAL" > "$out/logcat.txt" || true
if [ -s "$out/crash.log" ]; then
  echo "smoke: CRASHES"
  head -60 "$out/crash.log"
else
  echo "smoke: no crashes in the crash buffer"
fi
echo "smoke: requests seen by the mock server"
cat "$out/mock-server.log"
