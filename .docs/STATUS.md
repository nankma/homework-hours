# Status (as of 2026-10-10)

Live. The wireless hub is behind the CM4, all wireless DNS goes through AdGuard Home, and
Homework Hours enforces the parents' rules. Everything comes back on its own after a reboot
(~20 s, tested).

## Physical setup
- Q1000K 1G port → CM4 **eth1**; CM4 **eth0** → wireless hub. Wired servers stay on the Q1000K.

## CM4 (OpenWrt SNAPSHOT r35679, kernel 6.12.100, aarch64)
| Item | State |
|---|---|
| Bridge (`firmware/files/etc/uci-defaults/99-cm4-bridge`) | br-lan = eth0 + eth1, STP, `192.168.0.2/24`, DHCP/RA off, Wi-Fi off, TZ Los Angeles, SSH key-only |
| Extra packages | adguardhome, curl, ip-bridge, tcpdump-mini, fdisk. OpenAppFilter built but not installed |
| AdGuard Home 0.107.78 | DNS :5353; UI/API on 127.0.0.1:3000 only (SSH tunnel). Users `admin` (parent), `pcapi` (app). Clients: one `HH <child>` per child, managed by the app |
| DNS redirect (`parentalcontrol`) | Enabled at boot; nft tables `bridge pc_dns` + `inet pc_dns` |
| LuCI (uhttpd) | 127.0.0.1 only (SSH tunnel) |
| homework-hours | Enabled at boot, `:8443`; steps 1–5 deployed |
| Storage | Data partition `mmcblk0p3` (ext4, 14 GB) on `/mnt/data` (AdGuard work dir); query log ≤14 days, statistics 14 days; root overlay ~38 MB free |
| Backups | `sysupgrade -b` archives kept locally in `backups/` (not in the repo: they hold secrets) |

## Homework Hours
- **Step 1:** HTTPS with a home CA, AdGuard-account login, sessions, rate limit.
- **Step 2:** settings API with validation and optimistic locking; children, rules, site groups,
  calendar, exceptions, favorites; responsive page.
- **Step 3:** device discovery (bridge FDB + ARP + sweep, names from the router's reverse DNS),
  Devices tab, per-device activity from the AdGuard query log.
- **Step 4:** enforcement: rule engine in Go, reconciler (every minute, on save, at startup) →
  AdGuard clients and rules + nft internet-off. Verified with real DNS: a rule blocks, an open
  exception overrides it, a block exception blocks on its own.
- **Step 5:** school district calendar synced at startup and 03:00 daily, real break ranges,
  change notices, "Sync now".
- **New devices** (a child's device whose MAC changed) are highlighted on the Devices tab.
- Children's devices use their hardware MAC (private Wi-Fi address off).

## Open
Nothing waiting for a decision. Remaining work and ideas are tracked locally (not in the repo).
