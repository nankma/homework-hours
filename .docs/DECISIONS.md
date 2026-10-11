# Decision log

Newest last. "Rejected" lists what was considered and why it lost.

## 2026-08 Firmware
- **Build OpenWrt ourselves in WSL** (main @ r35679, `rpi-4` profile) instead of on the Pi 4
  (too little RAM) or a release image (wanted the same tree for later packages).

## 2026-10-09 Network role
- **The Q1000K stays the router (NAT + DHCP, 192.168.0.1); the CM4 is a transparent L2 bridge
  with management IP 192.168.0.2.**
  Rejected: CM4 as main router behind Q1000K bridge mode, because in bridge mode the Q1000K
  gives internet to only one device, which would waste its 10G port for the wired servers.
- **Only the wireless segment is controlled.** Wired servers go on the 10G port, wireless hub
  behind the CM4 on the 1G port. All family devices are wireless.
- **Blocked devices can still reach local servers**; only internet is limited.
- **DHCP reservations on the Q1000K: `.21`–`.50`** (Q1000K requires reservations inside its pool,
  pool `.21`–`.200`). Not needed for kids' devices: the app tracks MAC → IP.
- **CM4 Wi-Fi stays off**; management via the bridge IP only.
  Rejected: CM4 joins home Wi-Fi for management (two paths into the same LAN, confusing).

## 2026-10-09 Filtering engine
- **AdGuard Home** as the DNS engine (service catalog, per-client settings, query log, API).
  Rejected: Pi-hole (no service catalog/schedules), Blocky (more hand config), Technitium
  (heavy), warden/kidsfirewall (IP sets break shared Google IPs), luci parental apps
  (whole-device only), NextDNS (cloud). OpenAppFilter (DPI) kept for a later experiment.
- **DNS reaches AdGuard by transparent redirect on the bridge**, nothing set on devices or in the
  Q1000K's DHCP (that would also hit wired servers and break DNS for everyone if the CM4 died).
- **No kernel change** (no `br_netfilter`): bridge-family nft re-addresses DNS frames to the CM4,
  inet nat redirects them. Packages are installed with apk from the same build tree, no reflash.

## 2026-10-09 Product: Homework Hours
- **Own web UI** instead of the AdGuard UI (too complicated for daily use); phone + PC.
- **Rules are an ordered list per child** (dates × sites × weekday windows, first match wins,
  default allow). "Always allowed / always blocked" lists were dropped as redundant.
- **Site groups** of services, websites and categories; a hand-added site wins over a service.
- **Exceptions are simple:** start now, "for how long" (30 min … 12 h), one per child (new
  replaces old), expired ones vanish, favorites for one click. No start/end time pickers.
- **School calendar** synced from the Bellevue School District ICS (Holidays and Closures);
  break ranges read from the feed's recurrence rules (the confirm step was dropped in step 5);
  sync daily at 03:00; changes shown as notices until dismissed; applies to all children; no
  early-release sync.
- **Devices tab** shows children's devices always, others seen in the last 7 days; activity
  ranges 30 min / 1 h / 2 h / 4 h / 1 day / 1 week; subdomains merged; system traffic hidden;
  blocked vs not blocked.

## 2026-10-09 Implementation
- **Go single binary, stdlib only**, cross-compiled with the tree's Go; UI embedded.
  Rejected: shell CGI under uhttpd (the earlier plan) — too limited for this UI and logic;
  Rust (extra cross toolchain and crates, performance not needed); Python (interpreter and
  modules on a 33 MB-free overlay, third-party TLS/x509). Details: DESIGN §3.3.
- **Login reuses AdGuard Home accounts** (verified via `/control/status`); no second password.
- **Children's devices are not blocked from reaching the CM4**; the AdGuard password protects.
- **HTTPS from the start** with a self-made home CA (parents install it once).
- **Settings in one JSON file** with whole-document saves and optimistic locking.
- **One responsive page** for phone and desktop instead of two layouts.

## 2026-10-09 Enforcement (step 4 design)
- **Our own minute reconciler** drives AdGuard; AdGuard's built-in schedule is not used.
- One AdGuard client `HH <child>` per child (ids = device IPs); groups → blocked services,
  `$client` user rules in our own block, parental filter; Internet-off via nft bridge drop to
  the Q1000K MAC.
- Accepted: 1-minute granularity; the app owns `HH …` clients and its rule block (manual edits
  are reverted); AdGuard `cache_ttl_max` 60 s; a sync-status panel.
- Parked: allowing a site inside a blocked service (e.g. "youtube.com/edu"); paths are invisible
  to DNS anyway.
- **AdGuard UI cannot be disabled alone** (same server as the API), so it and LuCI listen on
  127.0.0.1 only (decided and done 2026-10-10: AdGuard `http.address: 127.0.0.1:3000`, uhttpd
  `listen_http 127.0.0.1:80`, `listen_https 127.0.0.1:443`). Access through an SSH tunnel. Also
  ends plain-HTTP password exposure on Wi-Fi.

## 2026-10-09 Devices (step 3)
- Discovery from the bridge forwarding table + ARP, with a periodic ARP sweep; names from the
  Q1000K's reverse DNS. Network facts in `seen.json`, not `state.json` (no version churn).
- Devices on the home-network side can be named and assigned before go-live.
- OpenAppFilter as an alternative/second engine, with an engine switch: investigate later (TODO).
- **Devices that change MAC** (2026-10-10): highlight new devices (chosen); a default rule set for
  unknown devices was not chosen for now.

## 2026-10-09 Storage
- **Data partition** on the free 14.4 GB (`mmcblk0p3`, ext4, `/mnt/data`) for AdGuard's work dir;
  settings stay in `/etc` so sysupgrade backups carry them. Fallback to tmpfs if p3 is missing.
- **Logs kept at most ~14 days**: AdGuard query log rotation every 7 days (two files), statistics
  14 days. Asked for by the user to keep disk use bounded.

## 2026-10-10 License
- **GPL-3.0-or-later plus dual licensing.** Anyone may build and run it themselves; the owner
  also sells ready-made devices, installation services and commercial licenses. Contributions
  need a contributor agreement so dual licensing stays possible.
  Rejected: MIT (anyone could sell it closed), keeping it closed (no do-it-yourself use).
- The app only talks to AdGuard Home over HTTP and to nftables via `nft`, so it is a separate
  work from those GPL components; modifying them or writing kernel modules (e.g. for
  OpenAppFilter) would bring GPL-2.0/3.0 obligations for that part.

## 2026-10-10 No ad blocklists in AdGuard Home
- **Blocklists off; AdGuard Home only does parental control** (per-client blocked services,
  user rules, adult filter, safe search). The default AdGuard DNS filter blocked ads for every
  wireless device, which set off "disable your ad blocker" walls and broke tracking links.
  Ad blocking is left to each browser's extension, where the user can turn it off per site.
