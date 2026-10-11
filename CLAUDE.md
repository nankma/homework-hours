# CM4 OpenWrt parental-control bridge

Waveshare CM4-DUAL-ETH-BASE running OpenWrt SNAPSHOT r35679 (kernel 6.12.100, aarch64,
package manager `apk`). It is a transparent L2 bridge between the home router (Q1000K, 1G LAN
port) and the wireless hub, and runs the Homework Hours parental-control app.

**Start with `.docs/RESUME.md`.** Project docs live in `.docs/` (README, DESIGN, STATUS,
DECISIONS, OPERATIONS, REBUILD, DATA-AND-API); `firmware/README.md` explains the firmware files.
Keep `STATUS.md` current when things change. Machine-specific notes (paths, users, local-only
files, the work list) are in `CLAUDE.local.md` when present.

## Access

- SSH: `ssh -i ~/.ssh/cm4pi_rsa root@192.168.0.2` (key only; password auth is off).
  Use an RSA key: Windows OpenSSH fails with ed25519 against this dropbear.
- AdGuard Home UI and LuCI listen on 127.0.0.1 only. Open them through an SSH tunnel:
  `ssh -i ~/.ssh/cm4pi_rsa -L 3000:127.0.0.1:3000 -L 8080:127.0.0.1:80 root@192.168.0.2`, then
  `http://localhost:3000` (AdGuard) and `http://localhost:8080` (LuCI). The app reaches AdGuard
  on 127.0.0.1:3000.
- Fallback when the LAN path is broken: a cable from a PC with static `192.168.0.250/24`
  (no gateway) to CM4 **eth0**, then `root@192.168.0.2`.
- BusyBox userland: no `nohup`, no `ip -br`. To survive a network reload over SSH, run
  `sh -c "sleep 1; /etc/init.d/network reload" </dev/null >/tmp/x.log 2>&1 &`.

## Building and installing extra packages (no reflash)

The WSL build tree (`~/openwrt/openwrt` of the build user, never root) is the tree that
produced the running image. Its kernel vermagic `df0c3e7257d9a8cf652064c77493e840` matches the
CM4, and its signing key is already trusted by the CM4. So packages built there install with
plain `apk add`.

1. Add the package to `firmware/config.seed` as `=m`. **Never add anything that changes the
   kernel config** (e.g. `kmod-br-netfilter`, `kmod-nft-bridge`): that changes the vermagic,
   and then the kmods no longer match the running kernel and a reflash is needed.
   Out-of-tree kmods whose dependencies are already in the kernel (e.g. `kmod-oaf`) are fine.
2. Build in WSL: `bash <repo>/firmware/build.sh` (several minutes when Go/AdGuard must
   rebuild; log in `build.log.pkgs` in the tree). The script fails if the vermagic changed.
3. Find dependencies the CM4 lacks: `staging_dir/host/bin/apk adbdump <file.apk>` (depends
   list) vs `apk info -e <name>` on the CM4. Pick the `.apk` files from `bin/packages/...` and
   `bin/targets/bcm27xx/bcm2711/packages/` (kmods).
4. `scp -O ... root@192.168.0.2:/tmp/apks/` and on the CM4 `apk add --no-network /tmp/apks/*.apk`.

Don't run `./scripts/feeds update -a` in the tree: it moves the other feeds off the versions
the image was built from. Only the `oaf` feed was added (`feeds.conf`).

## Repo layout

- `app/`: Homework Hours (Go). `firmware/`: firmware config, package build, and the `files/`
  scripts that run on the CM4 (see `firmware/README.md`). `.docs/`: documentation.

## Network invariants (don't break these)

- The CM4 must never serve DHCP/RA (`dhcp.lan.ignore=1`, odhcpd disabled). It sits on the
  household LAN.
- `br-lan` = eth0 (to the hub) + eth1 (to the router), STP on as loop protection. Never cable
  the hub to both the router and the CM4.
- Wi-Fi on the CM4 stays off; management goes through the bridge IP.
