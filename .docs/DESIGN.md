# Design

## 1. Requirements

### Goals
- **Per-child, time-based blocking per site group.** Main case: YouTube (and similar) blocked on
  school days before 5 PM while the rest of the internet stays open for homework.
- **Open early / block now.** A parent can override the schedule for a while ("homework done,
  open video for 1 hour").
- **School calendar aware.** Holidays and breaks (Bellevue School District) change which rules
  apply.
- **Only the wireless segment is controlled.** All family devices are wireless. Wired servers
  (file server etc.) are never touched.
- **One family web page** for phone and desktop; the AdGuard UI is too complicated for daily use.
- Several children, several devices per child.

### Functional requirements (agreed with the user)
- **Children** each have an ordered **rule list**. A rule = *Dates* (any of: every day, a calendar
  kind, or a named calendar entry) + *Sites* (any of: Internet, or site groups) + *per-weekday
  open times*. Rules are checked top to bottom; for each site group the first matching rule
  decides. No matching rule → allowed. A weekday left unchecked means the rule doesn't apply
  that day. Internet blocked → every site blocked.
- **Site groups** bundle AdGuard catalog services (YouTube, TikTok…), websites (domains) and
  categories (adult content). A domain belongs to at most one group; a hand-added website wins
  over a service that also covers it.
- **Exceptions**: temporary allow or block of chosen groups (or Internet) for one child, starting
  now, for 30 min / 1 / 2 / 3 / 4 / 6 / 12 hours. One per child: a new one replaces the old.
  Expired ones disappear. They can be saved as **favorites** for one-click start.
- **Calendar**: district holidays and breaks (synced daily from the district ICS feed, with
  real date ranges; the parent may still change a break's last day or ignore an entry) plus
  hand-added entries (holiday / break / custom). Early-release days are not synced. When a sync
  changes something, the parent sees a notice until they dismiss it.
- **Devices**: every device seen on the wireless side is listed; a parent assigns it to a child.
  Children's devices are always listed; others only if seen in the last 7 days. Per device, the
  sites it visited (from the AdGuard query log) over 30 min / 1 h / 2 h / 4 h / 1 day / 1 week,
  subdomains merged under the main domain, background/system traffic hidden by default, split
  into "blocked now" and "not blocked", each with "add to group".
- **Pause** all internet for a child; **safe search** per child.
- **Login** with the existing AdGuard Home accounts (no second password set). HTTPS from day one.
  Children's devices are *not* blocked from reaching the CM4; the AdGuard admin password is the
  protection.

### Non-goals
- Path-level control (e.g. `youtube.com/edu`): DNS sees only host names. Parked in TODO.
- Cellular data, VPN apps, and DoH to hard-coded IPs can bypass DNS blocking. Accepted.

## 2. Network topology

```
ISP fiber
  └─ Q1000K SmartNID: router, NAT + DHCP for 192.168.0.0/24, gateway 192.168.0.1
       ├─ 10G LAN → wired servers (not controlled)
       └─ 1G LAN  → CM4 eth1 (PCIe RTL8168h)
                      ║  br-lan: transparent bridge, STP on, mgmt IP 192.168.0.2/24
                    CM4 eth0 (bcmgenet)
                      └→ wireless hub (pure AP, no NAT/DHCP) → phones, tablets, laptops, consoles
```

- The Q1000K **stays the router**: in its bridge mode only one device behind it gets internet,
  which would waste the 10G port.
- All devices share one flat network, so wireless devices still reach the servers (SMB, mDNS).
- The CM4 **never serves DHCP or RA** (`dhcp.lan.ignore=1`, odhcpd disabled). It has a static
  `192.168.0.2`, gateway/DNS `192.168.0.1`. Service-cable fallback: a PC with static
  `192.168.0.250/24` (no gateway) on CM4 **eth0**, then `root@192.168.0.2` (the bridge IP answers
  on either port).
- STP is on as loop protection. Never cable the hub to both the Q1000K and the CM4.
- CM4 Wi-Fi is off; management goes through the bridge IP.
- **Fail-open fallback:** if the CM4 dies, plug the hub straight into the Q1000K 1G port.

### Address plan (on the Q1000K)
| Range | Use |
|---|---|
| `.1` | Q1000K |
| `.2` | CM4 |
| `.3`–`.20` | Manually configured static IPs (servers) |
| `.21`–`.200` | Q1000K DHCP pool (its reservations must be inside the pool; `.21`–`.50` for reservations) |

Reservations are **not** needed for children's devices: the app tracks MAC → IP itself.

## 3. Architecture

```
            parent phone / PC
                   │ HTTPS :8443 (home CA)
┌──────────────────▼─────────────────────────── CM4 (OpenWrt) ───────────────────────────┐
│  homework-hours (Go, single binary, procd service)                                      │
│   ├ web UI (embedded)  ├ JSON API  ├ sessions/login ──► AdGuard /control/status (Basic) │
│   ├ state.json store                                                                    │
│   ├ [step 3] discovery: ARP/neigh + bridge FDB (MAC↔IP, port) ; AdGuard query log       │
│   ├ [step 4] reconciler (every minute + on save) ──► AdGuard API (clients, user rules)  │
│   │                                             └──► nft table "bridge hh" (internet off)│
│   └ [step 5] district calendar sync (03:00 daily + startup + "Sync now")              │
│                                                                                         │
│  AdGuard Home :5353 DNS, :3000 UI/API  ◄── nft "pc_dns": wireless DNS redirected here   │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 Transparent DNS redirect (live, `parentalcontrol` init script)
The kernel has no `br_netfilter`, so bridged frames never reach the inet hooks. Two nft tables
(template `firmware/files/usr/share/parentalcontrol/dns-redirect.nft.in`):
1. `table bridge pc_dns`, prerouting, only frames entering on **eth0** (wireless side):
   drop IPv6 DNS and DNS-over-TLS (853); for IPv4 port 53 set mark `0x5353`,
   `meta pkttype set host` and `ether daddr set <br-lan MAC>` so the frame is delivered to the
   CM4's own stack instead of being bridged.
2. `table inet pc_dns`, nat prerouting: marked port-53 traffic → `redirect to :5353`; DNS sent
   directly to the CM4 (`fib daddr type local`) → also `:5353`.

Conntrack reverses the NAT, so devices see answers from the server they asked (192.168.0.1).
AdGuard sees each device's real IP. Devices and the Q1000K's DHCP settings are unchanged.
Optional `only_mac` list in `/etc/config/parentalcontrol` limits redirect to chosen MACs.
`/etc/init.d/parentalcontrol trial [sec]` applies with auto-revert unless `confirm`ed.

### 3.2 AdGuard Home
- 0.107.78 (OpenWrt package), config `/etc/adguardhome/adguardhome.yaml`, work dir
  `/mnt/data/adguardhome` (data partition, see §3.5). DNS on `0.0.0.0:5353`, UI/API on `127.0.0.1:3000`
  (plain HTTP, local only; parents use an SSH tunnel).
- Upstreams: Quad9 DoH + Cloudflare DoH in parallel, Quad9 plain as bootstrap, Q1000K
  (`192.168.0.1`) as fallback. Firefox canary `use-application-dns.net` → NXDOMAIN.
- Users: `admin` (the parent) and `pcapi` (service account for the app; password in
  `/etc/parentalcontrol/agh.secret`, mode 600, format `user:password`).
- Query log rotated every 7 days (at most ~14 days on disk); statistics kept 14 days.

### 3.3 Homework Hours (`app/`)
- Go 1.26, stdlib only, `CGO_ENABLED=0` linux/arm64, ~7 MB. Built with the Go toolchain from
  the OpenWrt build tree (`app/build.sh`), runs `go vet` and `go test` first.
- `/usr/bin/homework-hours -listen :8443 -data /etc/homework-hours`, procd init script
  `/etc/init.d/homework-hours` (START=95, respawn).
- Files (`main.go` wiring; `tlscert.go`; `auth.go` sessions + limiter + login; `adguard.go`;
  `store.go` state + optimistic locking; `validate.go`; `server.go` routes + security headers).

#### Why Go (and not Rust or Python)
| Need | Go | Rust | Python |
|---|---|---|---|
| Nothing extra to install on the CM4 (root overlay has ~33 MB free) | One static binary (~7 MB), no runtime | One static binary, smaller | Needs the Python interpreter + modules on the CM4 (tens of MB, from the same feeds), plus a compiled `cryptography` module for the CA |
| Toolchain | **Already in the OpenWrt build tree** (built for AdGuard Home); cross-compiling is `GOOS=linux GOARCH=arm64` | Extra cross toolchain (rustup target, musl/aarch64 linker) to set up and keep working | None to build, but packaging for apk must match the image's feeds |
| Libraries for this job (HTTPS server, TLS + x509 CA generation, JSON, HTTP client, embedded files) | **All in the standard library**: zero third-party dependencies | Several crates (HTTP server, rustls, rcgen, serde…) to choose, vendor and update | HTTP/TLS/x509 need third-party packages; stdlib server is not meant for production |
| Fit with the rest | Same language as AdGuard Home; simple concurrency for the minute loops | Strongest safety/performance, but neither is needed at this scale (a few requests per minute) | Fastest to prototype; slower start, higher RAM |
| Iteration speed | Builds in seconds, simple code | Slower compiles, steeper code | Fast |

Go gives a single dependency-free binary built with a toolchain we already have, which keeps
the CM4 clean and the build reproducible. The cost: a larger binary than Rust (fine for the
space we have) and less compile-time safety than Rust (acceptable for a small home app with
tests). The first plan, shell CGI scripts under uhttpd, was dropped as too limited for this UI.

### 3.4 Authentication and TLS
- **Login** = HTTP Basic against AdGuard `/control/status` with the entered name/password.
  The `pcapi` service account is refused for the web page.
- **Rate limit (ours):** 5 failures per client IP → 15 minutes locked (HTTP 429). AdGuard's own
  limiter can't help: all our checks come from 127.0.0.1.
- **Session:** random 256-bit token, cookie `hh_session` (HttpOnly, Secure, SameSite=Strict),
  30 days, persisted in `sessions.json` so restarts don't log parents out.
- **CSRF:** every non-GET API call needs header `X-HH: 1` (a cross-site form can't send it) on
  top of SameSite=Strict.
- **Headers:** HSTS, CSP (self + Google Fonts), `X-Frame-Options: DENY`, nosniff, no-referrer.
- **TLS:** the app makes a home CA (ECDSA P-256, 10 years) and a server leaf (800 days,
  re-issued automatically within 30 days of expiry). SANs: `192.168.0.2`, `192.168.1.1` (the old service address, unused),
  `cm4-bridge`, `cm4-bridge.lan`. Parents install `/ca.crt` once per device (iOS also needs
  "full trust" under Settings → General → About → Certificate Trust Settings).

### 3.5 Storage
SD card layout (16 GB card, standard OpenWrt Raspberry Pi image):

| Partition | Size | Mounted | Contents |
|---|---|---|---|
| `mmcblk0p1` (FAT) | 64 MB | `/boot` | Pi firmware, kernel, `config.txt` (24 MB used) |
| `mmcblk0p2` | 104 MB | — | **Root partition**: holds two layers |
| ├ squashfs (read-only, compressed) | ~6 MB | `/rom` | The OpenWrt system as built; never changes until a reflash |
| └ ext4 overlay (writable, `loop0` in the rest of p2) | 86.7 MB | `/overlay` | Every change since flashing: installed packages (AdGuard 32 MB, homework-hours 7 MB), `/etc` config, our settings |
| `mmcblk0p3` (ext4, label `data`), sectors 360448–end | 14.4 GB | `/mnt/data` | AdGuard work dir `/mnt/data/adguardhome` (query log, statistics, filter lists). Created 2026-10-09 |

`/` is the overlayfs merge of `/rom` + `/overlay`: **38 MB free** (2026-10-09). `/tmp` is RAM
(tmpfs, ~450 MB, lost on reboot) and holds only OpenWrt runtime files; nothing of ours.

- **Mounting:** `/etc/init.d/datafs` (START=13, after logd, before AdGuard at 19) mounts p3 on
  `/mnt/data`. If p3 is missing it mounts a 64 MB tmpfs there instead and logs an error, so
  AdGuard never fills the small root overlay.
- **Log retention:** AdGuard keeps only two query-log files: `querylog.json` and
  `querylog.json.1`. Every hour it checks the oldest entry of the current file; once that is
  older than the rotation interval, the file is renamed to `.1`, replacing (deleting) the
  previous `.1`. With the interval at **7 days**, nothing older than about **14 days** is ever on
  disk, and at least 7 days are always available (the Devices tab needs at most 1 week).
  Statistics use a rolling **14-day** window. The system log is a RAM ring buffer (`logread`).
- **Reflash / sysupgrade:** a reflash wipes the overlay; sysupgrade with "keep settings" carries
  over the paths in `/etc/sysupgrade.conf` (our settings, init scripts, binary). Both rewrite the
  partition table: re-create p3 with `fdisk` at **start sector 360448** (end = default) and the
  data is back; until then `datafs` falls back to tmpfs.

Our files, JSON, no database: `/etc/homework-hours/state.json` (all settings, versioned),
`sessions.json`, `seen.json` (device network facts), TLS files. Writes are atomic (temp file + rename). Both `/etc/homework-hours`
and `/etc/parentalcontrol` are listed in `/etc/sysupgrade.conf`. The query log stays in
AdGuard; live device IPs stay in memory.

### 3.6 Web UI
One responsive page (tabs: Today, Exceptions, Schedules, Site groups, Calendar, Devices) that
loads `/api/state`, edits it in place, and after every change saves the whole state with the
version it loaded (optimistic locking; on conflict it reloads and asks the parent to redo).
Re-fetches every 60 s and when the tab becomes visible. Under 640 px wide: larger touch targets,
16 px inputs, single column, fewer table columns. The rule engine (status, first-match) runs in
the page for display; step 4 runs the same logic in Go for enforcement.

## 4. Enforcement (step 4)

### When
A reconciler inside the app runs **after every save**, **every minute** (window edges,
exception expiry, calendar days), **at startup**, and **when a child's device IP changes**. It
computes the desired state, compares it with AdGuard's actual state, and calls the API only for
differences (idempotent; manual edits to our objects are reverted within a minute).
Granularity is one minute (accepted).

AdGuard's own `blocked_services_schedule` is **not** used (one range per day, services only, no
calendar, no exceptions); it is cleared on our clients.

### Translation
Per child, evaluate now: paused → exception → first matching rule → default allow, for Internet
and every group. Then:

| Our setting | AdGuard / CM4 |
|---|---|
| Child | Persistent client `HH <name>`; `ids` = current IPs of the child's devices; `use_global_settings:false`, `use_global_blocked_services:false`, `filtering_enabled:true`, empty schedule |
| Service member of a blocked group | in the client's `blocked_services` |
| Website member of a blocked group | user rule `\|\|example.com^$client='HH <name>'` inside our block `! homework-hours begin` … `! homework-hours end` of the global user rules (set via `/control/filtering/set_rules`; other user rules untouched) |
| Category "adult" in a blocked group | client `parental_enabled: true` |
| Child safe search | client `safe_search` |
| Internet blocked (or paused) | nft table `bridge hh`: drop frames from the child's device MACs to the Q1000K's MAC (cuts open connections and hard-coded-IP apps too; LAN servers stay reachable) |
| Unassigned devices | untouched (AdGuard global settings) |

Example: Tuesday 15:30, Maya's video window is 17:00–20:00, adult always blocked, internet
07:00–21:30 → `HH Maya` gets `blocked_services=[youtube,tiktok,instagram,snapchat,twitch]`,
`parental_enabled=true`, `safe_search.enabled=true`; our rule block has
`||pornhub.com^$client='HH Maya'` and `||xvideos.com^$client='HH Maya'`; nft untouched. At 17:00
only a `clients/update` removing the five services is sent.

Ownership: clients named `HH …` and our rule block belong to the app (agreed). AdGuard
`cache_ttl_max` is set to 60 s so newly blocked names stop resolving within about a minute
(agreed). A "sync status" panel shows the last sync, AdGuard reachability, and what is blocked
for each child right now (agreed).

### Implementation (`app/engine.go`, `app/enforce.go`, `app/district.go`)
- **Engine:** a Go port of the page's `status()`; `engine_test.go` covers windows (end is
  exclusive), district holidays and break ranges, parent edits/ignores, exceptions, pause, and
  "internet off blocks every group". The district feed lives on the server
  (`state.district.feed`, see §4b); the page reads it from there, so both sides expand it
  identically.
- **Time zone:** `America/Los_Angeles` (flag `-tz`), from Go's embedded `time/tzdata`
  (OpenWrt has no zoneinfo). Runs at 2 s past each minute, and ~0.3 s after a save.
- **Client names:** `HH ` + the child's name with only letters, digits, spaces, `-`, `_`
  (spaces collapsed; `-<id>` added on a clash), so it is safe inside `$client='…'`.
  "Anna & Ben" → `HH Anna Ben`.
- **A child gets an AdGuard client only when at least one assigned device has a known IP**
  (AdGuard requires ids). Stale `HH …` clients are deleted first (a renamed child's old client
  still holds the IPs), then clients are updated/added. Compared fields: ids, filtering,
  parental, safe browsing (off), safe search, blocked services, empty schedule
  (`time_zone` only, i.e. AdGuard's pause schedule unused), upstreams, tags.
- **Internet off** = nft `table bridge hh`, chain `cut` on the bridge **forward** hook: frames from
  MACs in set `off` pass only if ARP, DHCP (udp 67), to the LAN subnets of br-lan, broadcast,
  multicast, or IPv6 link-local/multicast; all other IPv4/IPv6 is dropped. Reloaded atomically
  only when the text changes. Only frames that cross the bridge are affected, so it works once
  the hub is behind the CM4.
- **Sync status** (`GET /api/sync`): time, errors, AdGuard reachable, DNS redirect loaded (nft
  table `inet pc_dns` exists), online devices behind the CM4, and per child: client, IPs,
  devices (and how many are not behind the CM4), internet off, blocked groups, services, sites,
  adult, safe search. Shown on the Today tab and as a header banner.
- **Verified:** with a test child (`check_host` reasons, nft set, no repeated API calls) and with
  real DNS from an assigned device: a rule blocks, an open exception overrides it, a block
  exception blocks on its own, and removing them restores the normal rules.

## 4b. District calendar sync (step 5; `app/ics.go`, `app/district.go`)

- **Source:** `https://www.bsd405.org/fs/calendar-manager/events.ics?calendar_ids[]=2`
  ("Holidays and Closures"). Fetched at startup (+20 s, for NTP/DNS), daily at 03:00 local,
  hourly after a failure, and by "Sync now" (`POST /api/district/sync`).
- **Real ranges, no guessing:** multi-day closures are recurring all-day events (Winter Break =
  `RRULE:FREQ=WEEKLY;BYDAY=MO..FR;UNTIL=20270101T000000Z`). The parser expands DAILY/WEEKLY
  rules (INTERVAL, COUNT, UNTIL, BYDAY), EXDATE and STATUS:CANCELLED, then joins days at most 3
  apart (a weekend) into one item `{date, to, note}`. UNTIL is compared by its date and is
  inclusive: the district writes the last day as `<day>T000000Z`.
- **Entries:** titles containing "Break" are breaks, the rest holidays; holidays inside a break
  are dropped (Presidents Day inside Mid-Winter Break); one entry per first day (longest wins).
  Ids `d<first day>`. Same code in Go (`districtBase`) and the page (`districtBase`).
- **Parent edits** (`district.changed` = a new last day, `district.ignored`) are keyed by id and
  survive syncs. If the district moves an entry's first day, the edit no longer matches; the
  Calendar tab lists such edits with "Remove this edit", and the sync notice says so.
- **Notices:** each sync compares old and new entries and appends "Added / Changed / Removed"
  lines to `district.notices` (max 30), shown on the Calendar tab (and a hint on Today) until
  "Got it" (`POST /api/district/dismiss`).
- **Failure:** the old feed stays in use; `lastError`/`lastAttempt` are shown on the Calendar tab.
- **Ownership:** feed, sync times, error and notices are server-owned (kept on page saves);
  a sync bumps the state version, so open pages reload it.
- **Seed:** `app/seed/bsd-holidays-2026-10-09.ics` is embedded and used until the first sync.
  `district_test.go` checks the parser against it (Winter Break 12/21–1/1, Mid-Winter 2/15–2/19,
  Spring 4/12–4/16, Thanksgiving 11/26–27, 14 entries).

## 5. Device discovery
- **Discovery** (`app/discovery.go`), every minute: MACs learned on the bridge
  (`bridge -j fdb show br br-lan`, permanent/multicast entries skipped) with the port they were
  learned on, joined with IPs from `/proc/net/arp`. A MAC present in the forwarding table is
  "online" (entries age out after ~5 min); last seen is kept.
- **ARP sweep** every 5 minutes: one 1-byte UDP packet (port 9) to every address of br-lan's
  /24 networks. The kernel resolves each address first, which fills the neighbor table even for
  devices the CM4 never talks to. Harmless; no extra packages.
- **Names**: reverse DNS against the Q1000K (`192.168.0.1`), which answers with DHCP host names
  (e.g. "Annas-iPad", "XBOX"). The parent can rename a device; the stored name wins.
- **Side**: learned on `eth0` → "wireless (behind the CM4)"; otherwise "home network". Until
  the hub is moved behind the CM4, every device is on the home-network side; parents can name
  and assign devices ahead of time (the page warns that rules can't apply yet).
- **Storage**: network facts in `/etc/homework-hours/seen.json` (saved every 15 min, forgotten
  after 30 days unseen unless stored), so per-minute updates never bump the settings version.
  `state.json` keeps only devices with a parent-given name, a child, or a "reviewed" mark.
- Locally administered MACs (private Wi-Fi address) are flagged.
- **New devices (MAC-change problem):** a child's device whose MAC changes
  (private address turned back on, or "Rotating") becomes an unknown device that no rule covers.
  So discovery records `firstSeen` per MAC (devices known before 2026-10-10 have none and never
  count as new), and the page marks a device **New** when it is behind the CM4, first seen since
  then, seen this week, and has no child, no name and no "reviewed" mark. New devices are listed
  first with a banner on the Devices tab and a hint on Today. If a new device has the same DHCP
  host name as a child's device, the page says it is probably that device with a new address;
  a new device with a private address gets an extra warning. The parent assigns it to the child
  (or names it), or clicks "Not a child's" (`reviewed: true` in `state.devices`); "Mark all"
  does that for every new device. Not done: a default rule set for unknown devices.
- **Activity**: AdGuard query log for the device's current IP (strict search `"ip"`, exact
  client match), paged back to the chosen range, at most 20,000 entries; aggregated per host
  name (count, last time, filtered count; safe-search rewrites don't count as blocked). The page
  merges subdomains per base domain and hides system traffic. A week of a busy PC takes ~2 s.
- **Catalog**: AdGuard's 136 services with their domains (`/control/blocked_services/all`),
  cached a day; used for the site-group picker and to tell which group covers a domain.
## 6. Security notes
- Kids can reach the CM4 (decided). Protection = the AdGuard admin password (must be strong and
  unknown to the kids) + login rate limit.
- AdGuard UI and LuCI listen on 127.0.0.1 only (2026-10-10); parents use an SSH tunnel. From the
  LAN only DNS (53, redirected), SSH (22, key only) and the parent page (8443) are open.
- Physical bypass: plugging the hub into the Q1000K skips the CM4. Keep cabling out of reach.
- Managed devices need a **stable MAC on this network**: private Wi-Fi address off or "Fixed"
  (iOS 18+), never "Rotating"; on Android "use device MAC" (or persistent randomization). A new
  MAC looks like a new, unassigned device, so rules stop following it.

## 7. Known limits
- Changes apply within about a minute; open connections may survive a DNS block until they
  reconnect (a playing video can continue). Internet-off is immediate (nft drop).
- Cellular data, VPNs, DoH on hard-coded IPs bypass DNS blocking. Blocking YouTube also breaks
  embedded YouTube videos on homework sites.
- Offline content (downloaded videos, offline games) isn't controlled.
- A browser's own "secure DNS" (DoH to a provider chosen by hand, e.g. in Chrome or Edge)
  bypasses AdGuard; only Firefox's automatic DoH is turned off (canary domain).

### Future improvements
These limits come from filtering by DNS. Planned work:
- **DoH hardening:** block the known DoH resolvers (names and IPs) for children's devices.
- **OpenAppFilter (OAF)** as a second engine: it identifies apps from the traffic itself (DPI,
  e.g. TLS SNI), so it doesn't depend on which DNS server a device uses. To investigate: whether
  it works on a bridge, its time rules, QUIC coverage; then an engine switch in the app (AdGuard
  or OAF) with the reconciler talking to whichever is enabled.
