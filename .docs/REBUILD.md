# Rebuild the CM4 from scratch

Step-by-step guide to reproduce this exact CM4: firmware build, flashing, base bridge config,
extra packages, AdGuard Home, the DNS redirect, and Homework Hours. Every version below is the
one running on the CM4 on 2026-10-09.

Files referenced live in this repo: `firmware/` (see [`firmware/README.md`](../firmware/README.md)
for what each file is), `app/`, `.docs/reference/adguardhome.yaml` (password hashes redacted).
Commands below use `<repo>` for where you cloned this repo, seen from WSL (e.g.
`/mnt/c/src/homework-hours`).

## Adapt to your setup first

| What | Where | Ours |
|---|---|---|
| Your SSH public key | create `firmware/files/etc/dropbear/authorized_keys` from `authorized_keys.example` (step 4) | not in the repo |
| CM4 address, router address, time zone, hostname | top block of `firmware/files/etc/uci-defaults/99-cm4-bridge` | `192.168.0.2/24`, `192.168.0.1`, `America/Los_Angeles`, `cm4-bridge` |
| Wireless-side port | `firmware/files/usr/share/parentalcontrol/dns-redirect.nft.in` | `eth0` |
| App: router, certificate names, time zone | flags `-gateway`, `-hosts`, `-tz` in `firmware/files/etc/init.d/homework-hours` | defaults in `app/main.go` |
| School calendar feed | `districtURL` in `app/district.go` | Bellevue School District |
| OpenWrt tree | `OPENWRT_TREE` for both `build.sh` | `~/openwrt/openwrt` (the default) |

Every address in this guide is ours; replace it with yours where it appears.

## 0. What you need

| Item | Ours |
|---|---|
| Carrier board | Waveshare **CM4-DUAL-ETH-BASE** (eth0 = CM4 native bcmgenet, eth1 = PCIe RTL8168h) |
| Module | Raspberry Pi **Compute Module 4 Lite** Rev 1.1, 1 GB RAM, Wi-Fi, no eMMC → boots from microSD |
| Storage | 16 GB microSD (image uses ~170 MB; the rest is unpartitioned) |
| Build host | Windows 11 + **WSL2 Ubuntu**, 12 cores, 8 GB RAM, ~40 GB free disk. A Raspberry Pi is too weak to build OpenWrt |
| Network | Quantum Fiber **Q1000K** as router (NAT/DHCP 192.168.0.0/24), a wireless hub in AP mode, 2 Ethernet cables |

## 1. Prepare the build host (WSL Ubuntu)

1. Stop WSL from appending the Windows `PATH` (a path with spaces broke the final packaging step):
   ```sh
   sudo tee /etc/wsl.conf <<'EOF'
   [interop]
   appendWindowsPath = false
   EOF
   ```
   then `wsl --shutdown` from Windows and reopen.
2. Build dependencies (run apt as root, everything else as a **normal user**; some host tools
   refuse to configure as root):
   ```sh
   sudo apt-get update
   sudo apt-get install -y build-essential clang flex bison g++ gawk gcc-multilib g++-multilib \
     gettext git libncurses-dev libssl-dev python3 python3-setuptools rsync swig unzip zlib1g-dev \
     file wget apache2-utils
   ```
   (`apache2-utils` is only for `htpasswd`, used in step 6.) The Waveshare wiki's package list
   is stale: `libncurses5-dev` → `libncurses-dev`, no python2.

## 2. Get the exact sources

```sh
mkdir -p ~/openwrt && cd ~/openwrt
git clone https://git.openwrt.org/openwrt/openwrt.git
cd openwrt
git checkout e9a6e45556e66067f7359509a7866cdbb8fe3f29     # main, r35679 "dropbear: bump to 2026.94"
cp <repo>/firmware/feeds.conf.pinned feeds.conf   # feeds pinned with ^<commit>
./scripts/feeds update -a
./scripts/feeds install -a
```

The pinned feeds include `oaf` (OpenAppFilter). On an existing tree **never** run
`./scripts/feeds update -a` with an unpinned `feeds.conf`: it moves feeds off these revisions.

## 3. Configure and build the firmware

```sh
cp <repo>/firmware/diffconfig .config
make defconfig
```

`diffconfig` = target `bcm27xx/bcm2711`, device profile **`rpi-4`** (the CM4 shares the Pi 4
image), LuCI, USB networking kmods, plus our extra packages as `=m` (built as packages, not in
the image): adguardhome, curl, ip-bridge, tcpdump-mini, fdisk, kmod-oaf, appfilter,
luci-app-oaf.

Optional, **untested**: bake the base config into the image so the first boot is already a
bridge. Create `firmware/files/etc/dropbear/authorized_keys` with your key first (step 4), and
adjust `99-cm4-bridge` (see "Adapt to your setup"):
```sh
mkdir -p files && cp -r <repo>/firmware/files/. files/ && rm -f files/etc/dropbear/authorized_keys.example
```
The running CM4 was built *without* this and configured live in step 5, which is the tested
path. Baking also puts the init scripts in the image before their programs exist; enable them
only after the matching steps (7b, 8, 9).

Build detached so a terminal disconnect doesn't kill it (takes about an hour the first time):
```sh
make download -j8
setsid make -j"$(nproc)" > build.log 2>&1 < /dev/null & disown
tail -f build.log
```

Check:
```sh
cat build_dir/target-*/linux-bcm27xx_bcm2711/linux-*/.vermagic    # must be df0c3e7257d9a8cf652064c77493e840
ls bin/targets/bcm27xx/bcm2711/
(cd bin/targets/bcm27xx/bcm2711 && sha256sum -c sha256sums --ignore-missing)
```

If the vermagic differs, kmods from this tree won't load on a CM4 flashed from a different
build; keep the image and its packages from the same tree.

## 4. Flash and first boot

1. Write `bin/targets/bcm27xx/bcm2711/openwrt-bcm27xx-bcm2711-rpi-4-squashfs-factory.img.gz`
   to the microSD card (e.g. Raspberry Pi Imager → "Use custom", or balenaEtcher; both take the
   `.gz` directly).
2. Insert the card, connect a PC to **eth0** with static IP `192.168.1.2/24`, power on.
3. Default OpenWrt is at `192.168.1.1` (LuCI over HTTP, SSH as root, no password yet). Set a
   root password: `ssh root@192.168.1.1` → `passwd`.
4. SSH key: generate an **RSA** key on Windows (`ssh-keygen -t rsa -b 4096 -f ~/.ssh/cm4pi_rsa`;
   Windows OpenSSH fails with ed25519 against this dropbear), then put **your** public key
   (`~/.ssh/cm4pi_rsa.pub`) in `/etc/dropbear/authorized_keys` on the CM4 (mode 600). The repo
   only has `authorized_keys.example`; never reuse someone else's key file.

## 5. Base bridge configuration

Skip if you baked `files/` into the image in step 3. Otherwise edit the top block of
`99-cm4-bridge` for your network, then:

```sh
scp -O -i ~/.ssh/cm4pi_rsa firmware/files/etc/uci-defaults/99-cm4-bridge root@192.168.1.1:/tmp/
ssh -i ~/.ssh/cm4pi_rsa root@192.168.1.1 'sh /tmp/99-cm4-bridge && reboot'
```

It sets (idempotent, safe to re-run): hostname `cm4-bridge`; timezone `America/Los_Angeles`
(`PST8PDT,M3.2.0,M11.1.0`); `br-lan` = eth0 + eth1 with STP (`forward_delay 4`); addresses
`192.168.0.2/24` and temporary `192.168.1.1/24`, gateway and DNS `192.168.0.1`, no IPv6
delegation; **DHCP, RA, DHCPv6 and odhcpd off** (the CM4 must never hand out addresses on the
home LAN); radio0 disabled; dropbear password logins off (key only) **if** a public key is in
`/etc/dropbear/authorized_keys` (otherwise it leaves password login on and logs a warning), so
install the SSH key (step 4) **before** running it. The temporary `192.168.1.1` keeps the service cable working
after the reboot.

Then cable **CM4 eth1 → Q1000K 1G port**. Check:
```sh
ssh -i ~/.ssh/cm4pi_rsa root@192.168.0.2      # from any home device
ping -c2 openwrt.org; date                    # internet + NTP time
logread | grep -i dhcp                        # no DHCP server activity from the CM4
```

## 6. Extra packages (from the same tree)

Packages were built in step 3 (`=m`). For a tree that already has an image flashed, use
`firmware/build.sh` (see `../CLAUDE.md`), which only adds packages and refuses to continue if
the kernel config changed.

Copy and install (the CM4 trusts the tree's package signing key because it came from it):
```sh
# in WSL: collect the .apk files of the packages and their missing dependencies
cd ~/openwrt/openwrt
find bin -name '*.apk' \( -name 'adguardhome-*' -o -name 'curl-*' -o -name 'libcurl*' \
  -o -name 'libnghttp2*' -o -name 'ip-bridge-*' -o -name 'tcpdump-mini-*' -o -name 'libpcap*' \
  -o -name 'fdisk-*' -o -name 'libfdisk*' \)
# dependencies of a package: staging_dir/host/bin/apk adbdump <file.apk>; on the CM4: apk info -e <name>
```
```sh
scp -O -i ~/.ssh/cm4pi_rsa <files> root@192.168.0.2:/tmp/apks/
ssh -i ~/.ssh/cm4pi_rsa root@192.168.0.2 'apk add --no-network /tmp/apks/*.apk'
```
Installed on our CM4: adguardhome 0.107.78-r1, curl 8.21.0, ip-bridge 6.18.0, tcpdump-mini
4.99.6, fdisk 2.42.2. (OpenAppFilter is built but **not installed**.)

Never add packages that change the kernel config (e.g. `kmod-br-netfilter`, `kmod-nft-bridge`):
the vermagic changes and kmods stop matching the running kernel. The design doesn't need them
(native nft `bridge` family is built in).

## 7. AdGuard Home

1. `/etc/init.d/adguardhome enable && /etc/init.d/adguardhome start`. Config file
   `/etc/adguardhome/adguardhome.yaml`, work dir `/opt/adguardhome` at first (moved to the data partition in step 7b; UCI `adguardhome.config`).
2. Run the setup wizard at `http://192.168.0.2:3000`: admin interface "All interfaces" port 3000;
   DNS on all interfaces, **port 5353** (53 belongs to the CM4's dnsmasq); create the parent's
   account (we use `admin`, strong password the children don't know).
3. Stop AdGuard and edit the YAML to match `.docs/reference/adguardhome.yaml`:
   - `dns.port: 5353`, `bind_hosts: [0.0.0.0]`
   - `upstream_dns`: `https://dns10.quad9.net/dns-query`, `https://cloudflare-dns.com/dns-query`;
     `upstream_mode: parallel`; `bootstrap_dns`: `9.9.9.10`, `149.112.112.10`, `2620:fe::10`,
     `2620:fe::fe:10`; `fallback_dns: [192.168.0.1]`
   - `filtering.blocked_services.schedule.time_zone: America/Los_Angeles`
   - `user_rules: ['||use-application-dns.net^$dnsrewrite=NXDOMAIN']` (turns off Firefox's
     automatic DoH)
   - query log `interval: 7d`, statistics `interval: 14d` (see step 7b)
   - service account for Homework Hours:
     ```sh
     htpasswd -bnBC 10 "" '<long random password>' | tr -d ':\n'   # in WSL → bcrypt hash
     ```
     add under `users:` as `- name: pcapi` / `password: <hash>`, and on the CM4:
     ```sh
     mkdir -p /etc/parentalcontrol
     printf 'pcapi:%s' '<same password>' > /etc/parentalcontrol/agh.secret
     chmod 600 /etc/parentalcontrol/agh.secret
     ```
4. Start AdGuard. Check from a Windows PC: `nslookup -port=5353 openwrt.org 192.168.0.2`.

Per-client settings in AdGuard only take effect with `use_global_settings: false` on the client.
Homework Hours manages its own clients (`HH <child>`) from step 4 of the app on.

## 7b. Data partition for AdGuard's logs

The image uses ~170 MB of the card; the root overlay has only ~38 MB free, too little for a
busy home's query log. Put AdGuard's work dir on a new partition in the free space:

```sh
printf 'n\np\n3\n360448\n\nw\n' | fdisk /dev/mmcblk0     # p3: first sector after p2 → end of card
grep mmcblk0p3 /proc/partitions || reboot                  # kernel usually sees it at once
mkfs.ext4 -q -L data -m 1 /dev/mmcblk0p3
```
Install the mount script and move AdGuard:
```sh
scp -O -i ~/.ssh/cm4pi_rsa firmware/files/etc/init.d/datafs root@192.168.0.2:/etc/init.d/datafs
# on the CM4:
chmod 755 /etc/init.d/datafs && /etc/init.d/datafs enable && /etc/init.d/datafs start
/etc/init.d/adguardhome stop
cp -a /opt/adguardhome /mnt/data/adguardhome && rm -rf /opt/adguardhome
sed -i 's#/opt/adguardhome/userfilters#/mnt/data/adguardhome/userfilters#' /etc/adguardhome/adguardhome.yaml
uci set adguardhome.config.work_dir=/mnt/data/adguardhome && uci commit adguardhome
/etc/init.d/adguardhome start
```
Log retention (in the YAML, AdGuard stopped): `querylog.interval: 7d` (two files rotate, so at
most ~14 days are kept) and `statistics.interval: 14d`. Add our files to
`/etc/sysupgrade.conf`: `/etc/homework-hours`, `/etc/parentalcontrol`, `/etc/init.d/datafs`,
`/etc/init.d/homework-hours`, `/etc/init.d/parentalcontrol`, `/etc/rc.d/S13datafs`,
`/etc/rc.d/S95homework-hours`, `/etc/rc.d/K10homework-hours`, `/etc/rc.d/S99parentalcontrol`,
`/etc/rc.d/K10parentalcontrol` (the rc.d links only exist after `enable`; list them anyway),
`/usr/bin/homework-hours`, `/usr/share/parentalcontrol`.

## 8. Transparent DNS redirect

```sh
K="-i ~/.ssh/cm4pi_rsa"
scp -O $K firmware/files/etc/config/parentalcontrol root@192.168.0.2:/etc/config/parentalcontrol
scp -O $K firmware/files/etc/init.d/parentalcontrol root@192.168.0.2:/etc/init.d/parentalcontrol
ssh $K root@192.168.0.2 'mkdir -p /usr/share/parentalcontrol && chmod 755 /etc/init.d/parentalcontrol'
scp -O $K firmware/files/usr/share/parentalcontrol/dns-redirect.nft.in root@192.168.0.2:/usr/share/parentalcontrol/
```
Then, **after** the wireless hub is behind the CM4 (step 10):
```sh
/etc/init.d/parentalcontrol trial 90     # applies; auto-reverts in 90 s unless confirmed
# check a wireless device still resolves names and shows up in AdGuard's query log
/etc/init.d/parentalcontrol confirm
/etc/init.d/parentalcontrol enable       # load at boot
```
How it works: DESIGN §3.1.

## 9. Homework Hours

```sh
# build (dev box, Git Bash); uses the Go from the OpenWrt tree: staging_dir/hostpkg/lib/go-1.26
MSYS_NO_PATHCONV=1 wsl.exe -d Ubuntu -u <user> -- bash <repo>/app/build.sh
# install
K="-i $HOME/.ssh/cm4pi_rsa"
scp -O $K app/bin/homework-hours root@192.168.0.2:/usr/bin/homework-hours
scp -O $K firmware/files/etc/init.d/homework-hours root@192.168.0.2:/etc/init.d/homework-hours
ssh $K root@192.168.0.2 'chmod 755 /usr/bin/homework-hours /etc/init.d/homework-hours
  printf "/etc/homework-hours\n/etc/parentalcontrol\n" >> /etc/sysupgrade.conf
  /etc/init.d/homework-hours enable; /etc/init.d/homework-hours start'
```
(`app/build.sh` uses the tree at `~/openwrt/openwrt`; override with `OPENWRT_TREE`, `GO`, `OUT`.) The Go toolchain exists in the tree because adguardhome is
built with it.

On first start the app creates `/etc/homework-hours/` with a home CA, a server certificate and
a default `state.json` (four site groups, no children). On each parent device: open
`https://192.168.0.2:8443/login`, download `/ca.crt`, install it as a trusted root (iOS: also
enable full trust under Settings → General → About → Certificate Trust Settings), sign in with
the AdGuard account.

## 10. Cabling and go-live

1. Q1000K: DHCP pool `192.168.0.21`–`.200`; leave `.2`–`.20` for static devices. Nothing else
   changes on the Q1000K.
2. Wired servers on the Q1000K **10G** port.
3. **Unplug the wireless hub from the Q1000K first**, then cable it to **CM4 eth0**. (Never both:
   STP would block a port, but don't rely on it.)
4. Wireless devices keep getting `192.168.0.x` from the Q1000K and have internet.
5. Enable the DNS redirect (step 8). Turn off "private Wi-Fi address" on the children's devices.
6. Remove the temporary `192.168.1.1/24` when you no longer need the service cable:
   `uci del_list network.lan.ipaddr=192.168.1.1/24; uci commit network`, then a detached
   `network reload`. The service cable then uses a static `192.168.0.250/24` and `192.168.0.2`.
7. Close the AdGuard UI and LuCI to the LAN: with AdGuard stopped, set `http.address:
   127.0.0.1:3000` in `/etc/adguardhome/adguardhome.yaml`; then
   `uci delete uhttpd.main.listen_http; uci add_list uhttpd.main.listen_http=127.0.0.1:80;
   uci delete uhttpd.main.listen_https; uci add_list uhttpd.main.listen_https=127.0.0.1:443;
   uci commit uhttpd; /etc/init.d/uhttpd restart`. Use them through an SSH tunnel (OPERATIONS).
8. Backup: `sysupgrade -b /tmp/cm4-config.tar.gz` and keep it off the CM4.

## 11. Verification checklist

- [ ] `ssh root@192.168.0.2` works with the key; password login refused.
- [ ] No DHCP offers from the CM4 (`logread`, and clients' lease server is `192.168.0.1`).
- [ ] `brctl show` / `bridge link`: eth0 and eth1 in br-lan, forwarding.
- [ ] Wireless device: internet works, its queries appear in AdGuard's log with its own IP.
- [ ] `nslookup use-application-dns.net` from a wireless device → NXDOMAIN.
- [ ] Wired servers unaffected; wireless ↔ server traffic works.
- [ ] `https://192.168.0.2:8443` shows a valid certificate on parent devices; sign-in works.
- [ ] After a reboot: bridge, AdGuard, DNS redirect and Homework Hours come back; clock syncs.

## Pitfalls we hit (so you don't)
- Building as root fails partway (GNU tar's configure refuses). Fix ownership, build as a user.
- WSL `PATH` import with spaces breaks `find -execdir` in packaging → `appendWindowsPath = false`.
- A detached build survives SSH disconnects but not a host reboot.
- Git Bash rewrites `/paths` passed to `wsl.exe` → prefix `MSYS_NO_PATHCONV=1`; send multi-line
  scripts through `bash -s` on stdin.
- OpenWrt BusyBox has no `nohup`; to survive a network reload over SSH use
  `sh -c "sleep 1; /etc/init.d/network reload" </dev/null >/tmp/x.log 2>&1 &`.
- Root overlay has only ~38 MB free with AdGuard installed: keep AdGuard's work dir on the data
  partition (step 7b). A reflash or sysupgrade rewrites the partition table; re-create p3 at
  sector 360448 without formatting.
