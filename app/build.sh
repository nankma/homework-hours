#!/bin/bash
# Cross-compile homework-hours for the CM4 (linux/arm64) with the Go toolchain from the OpenWrt
# build tree. Run in WSL as the build user:
#   wsl -d Ubuntu -u <user> -- bash <repo>/app/build.sh
# Overrides: OPENWRT_TREE (default ~/openwrt/openwrt), GO (the tree's Go), SRC (this app/ dir,
# needed only when the script is piped in through stdin), OUT (build dir).
set -euo pipefail
TREE=${OPENWRT_TREE:-$HOME/openwrt/openwrt}
GO=${GO:-$TREE/staging_dir/hostpkg/lib/go-1.26/bin/go}
if [ -f "${BASH_SOURCE[0]:-}" ]; then
	SRC=${SRC:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}
else
	SRC=${SRC:-/mnt/d/SR/CM4-OpenWRT/app}
fi
OUT=${OUT:-$HOME/homework-hours-build}

mkdir -p "$OUT"
cd "$SRC"
# Copy out of /mnt/d: building on the Windows mount is slow and trips over file modes
rsync -a --delete --exclude bin "$SRC/" "$OUT/src/"
cd "$OUT/src"
export GOCACHE="$OUT/cache" GOPATH="$OUT/gopath" GOFLAGS=-mod=mod GOTOOLCHAIN=local
"$GO" vet ./...
"$GO" test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 "$GO" build -trimpath -ldflags "-s -w" -o "$OUT/homework-hours" .
mkdir -p "$SRC/bin"
cp "$OUT/homework-hours" "$SRC/bin/homework-hours"
ls -l "$SRC/bin/homework-hours"
