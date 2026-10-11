#!/bin/bash
# Build extra packages for the CM4 in WSL, without changing the kernel, so they install on the
# running image. Run as the normal build user (never root):
#   wsl -d Ubuntu -u <user> -- bash <repo>/firmware/build.sh
# The OpenWrt tree defaults to ~/openwrt/openwrt (REBUILD.md step 2); override with OPENWRT_TREE.
# Output: bin/packages/... and bin/targets/.../packages/ (kmods) in the tree.
set -euo pipefail

TREE=${OPENWRT_TREE:-$HOME/openwrt/openwrt}
FW=$(cd "$(dirname "$0")" && pwd)
EXPECTED_VERMAGIC=df0c3e7257d9a8cf652064c77493e840   # kernel on the CM4 (apk list kernel)

[ "$(id -u)" -ne 0 ] || { echo "Do not run as root" >&2; exit 1; }
cd "$TREE"

# OpenAppFilter feed (only this feed is added/updated; the others stay pinned)
[ -f feeds.conf ] || cp feeds.conf.default feeds.conf
grep -q '^src-git oaf ' feeds.conf || echo 'src-git oaf https://github.com/destan19/OpenAppFilter.git' >> feeds.conf
[ -d feeds/oaf ] || ./scripts/feeds update oaf
./scripts/feeds install -p oaf -a >/dev/null

sed 's/\r$//' "$FW/config.seed" | grep '^CONFIG_' >> .config
make defconfig >/dev/null
while IFS= read -r line; do
	grep -qxF "$line" .config || { echo "NOT applied: $line" >&2; exit 1; }
done < <(sed 's/\r$//' "$FW/config.seed" | grep '^CONFIG_')

LOG=build.log.pkgs
make download -j8 > "$LOG" 2>&1
make -j"$(nproc)" IGNORE_ERRORS=m >> "$LOG" 2>&1 || true

vm=$(cat build_dir/target-*/linux-bcm27xx_bcm2711/linux-*/.vermagic)
if [ "$vm" != "$EXPECTED_VERMAGIC" ]; then
	echo "Kernel vermagic changed ($vm), kmods will NOT match the CM4" >&2
	exit 1
fi

echo "Built:"
find bin -newer "$FW/config.seed" -name '*.apk' \
	\( -name 'adguardhome*' -o -name 'curl*' -o -name 'libcurl*' -o -name 'ip-bridge*' \
	-o -name 'tcpdump-mini*' -o -name 'fdisk*' -o -name 'kmod-oaf*' -o -name 'appfilter*' \
	-o -name 'luci-app-oaf*' \) -printf '%p\n'
grep -E "ERROR: package/.* failed" "$LOG" || true
