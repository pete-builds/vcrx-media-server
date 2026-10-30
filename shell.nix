# Development shell for NixOS: run `nix-shell` at the repo root, then the
# commands in README.md and tv/README.md.
{ pkgs ? import <nixpkgs> { } }:

pkgs.mkShell {
  packages = with pkgs; [
    # Brand assets (brand/tools/make-assets.py). Keep this first: android-tools
    # brings its own python3, without Pillow, and the first one on PATH wins.
    (python3.withPackages (p: [ p.pillow ]))

    # Server. mkShell already brings gcc, which `go test -race` needs.
    go
    ffmpeg # the scanner and artwork tests call ffmpeg and ffprobe
    golangci-lint
    govulncheck

    # TV app. The APK builds in a container (tv/build-apk.sh), so there is no
    # JDK or Android SDK here, only the Docker client and adb.
    docker-client
    docker-buildx
    android-tools
  ];

  # go.mod pins the exact Go release, which nixpkgs may lag; let Go fetch it.
  GOTOOLCHAIN = "auto";
}
