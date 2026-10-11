# firmware/

Everything needed to rebuild the CM4's OpenWrt system and the scripts that run on it. The
step-by-step guide is [`.docs/REBUILD.md`](../.docs/REBUILD.md); this page says what each file is
and what you must change for your own setup.

Nothing here is build output: the OpenWrt build tree and its images live in WSL
(`~/openwrt/openwrt`), not in this repo.

## Build inputs

| File | What it is | REBUILD step |
|---|---|---|
| `feeds.conf.pinned` | OpenWrt package feeds pinned to the exact commits our image was built from (copy to `feeds.conf`) | 2 |
| `diffconfig` | The firmware configuration: target `bcm27xx/bcm2711`, profile `rpi-4`, LuCI, our extra packages as `=m` (copy to `.config`, then `make defconfig`) | 3 |
| `config.seed` | Extra packages to build later without touching the kernel config | 6 |
| `build.sh` | Builds the `config.seed` packages in an existing tree and refuses to continue if the kernel vermagic changed. Tree: `~/openwrt/openwrt`, or set `OPENWRT_TREE` | 6 |

## Files for the CM4 (`files/`, same paths as on the device)

| File | What it does | REBUILD step |
|---|---|---|
| `etc/uci-defaults/99-cm4-bridge` | Base config: transparent bridge eth0 + eth1, static address, **no DHCP/RA**, Wi-Fi off, SSH key-only (only if a key is installed). Idempotent | 5 (or baked in, step 3) |
| `etc/dropbear/authorized_keys.example` | Template for the SSH public key(s) allowed to log in | 4 |
| `etc/init.d/datafs` | Mounts the data partition (`/dev/mmcblk0p3` → `/mnt/data`), falls back to tmpfs | 7b |
| `etc/config/parentalcontrol` | DNS redirect switch: `enabled`, AdGuard `port`, optional `only_mac` list | 8 |
| `etc/init.d/parentalcontrol` | Loads/removes the DNS redirect; `trial [sec]` auto-reverts unless `confirm`ed | 8 |
| `usr/share/parentalcontrol/dns-redirect.nft.in` | nftables template of the redirect (DNS from the wireless port → AdGuard, DoT and IPv6 DNS dropped) | 8 |
| `etc/init.d/homework-hours` | procd service for the Homework Hours app (`/usr/bin/homework-hours`) | 9 |

## What you must change

| Where | What | Ours |
|---|---|---|
| `files/etc/dropbear/authorized_keys` | **Create it** from `authorized_keys.example` with *your* public key. It is not in the repo on purpose | — |
| `files/etc/uci-defaults/99-cm4-bridge`, top block | Hostname, time zone, the CM4's static address (free, outside the router's DHCP pool), the router's address, the temporary service-cable address | `cm4-bridge`, `America/Los_Angeles`, `192.168.0.2/24`, `192.168.0.1`, `192.168.1.1/24` |
| `files/usr/share/parentalcontrol/dns-redirect.nft.in` | Port facing the wireless hub, if not `eth0` | `eth0` (the CM4's native port) |
| App flags in `files/etc/init.d/homework-hours` (see `homework-hours -h`) | `-gateway` (your router, for device names), `-hosts` (names/IPs in the TLS certificate), `-tz` | defaults: `192.168.0.1`, `192.168.0.2,…`, `America/Los_Angeles` |
| `app/district.go` (`districtURL`) | School calendar feed (iCalendar), if you're not in the Bellevue School District | BSD "Holidays and Closures" |
