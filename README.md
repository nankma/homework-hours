# homework-hours

Per-child, time-based parental control on an OpenWrt CM4 bridge (AdGuard Home + nftables, Go web app)

**Homework Hours** turns a Raspberry Pi Compute Module 4 on a dual-Ethernet carrier board into a
transparent bridge between the home router and the wireless access point. Every wireless
device's DNS goes through AdGuard Home on the CM4, and a small web app lets parents say things
like *"YouTube and games are blocked on school days until 6 PM, open on weekends, and closed
for everyone at 9:30 PM"*, and open them early when the homework is done.

- **Rules per child and per site group**: an ordered list of rules (dates × sites × open times
  per weekday), first match wins. Groups bundle AdGuard's service catalog (YouTube, TikTok,
  Roblox…), websites and the adult-content filter.
- **Exceptions in one tap**: open or block a group for 30 minutes to 12 hours, with favorites.
- **School calendar aware**: holidays and breaks are synced nightly from the school district's
  iCalendar feed, so "no-school day" rules apply on their own.
- **Pause the internet** per child (cuts connections at the bridge, not just DNS).
- **Sees every device**: discovers devices behind the bridge, shows which sites each one used,
  and flags new devices (for example a child's phone that changed its MAC address).
- **No changes on the router or the devices**: DNS is redirected transparently on the bridge;
  wired servers are untouched. If the CM4 fails, plug the access point back into the router.

## Contents

- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Install](#install)
- [Usage](#usage)
- [Limitations](#limitations)
- [Documentation](#documentation)
- [Repository layout](#repository-layout)
- [Contributing](#contributing)
- [License](#license)

## How it works

```
ISP → home router (NAT + DHCP) ─┬─ wired servers                         (not controlled)
                                └─ CM4 eth1 ═ br-lan bridge ═ CM4 eth0 → Wi-Fi access point → family devices
                                       │
                                       ├─ nftables: wireless DNS → AdGuard Home; "internet off" drops
                                       └─ Homework Hours (Go): rules → AdGuard clients/rules every minute
```

The app keeps the parents' settings in one JSON file, evaluates the rules every minute (and
right after every change), and translates them into one AdGuard Home client per child (blocked
services, per-client domain rules, adult filter, safe search) plus an nftables set for
"internet off". Details: [design](.docs/DESIGN.md).

## Requirements

- Waveshare **CM4-DUAL-ETH-BASE** with a Raspberry Pi **Compute Module 4** (1 GB RAM is enough)
  and a microSD card (16 GB).
- A home router that keeps doing NAT and DHCP, and a Wi-Fi access point (AP mode, no NAT) for
  the devices to control.
- A build host for OpenWrt and Go: Linux or WSL2 Ubuntu, ~40 GB free disk.
- Children's devices with a fixed MAC address on the home network ("private Wi-Fi address" off).

## Install

The full, tested procedure is in [.docs/REBUILD.md](.docs/REBUILD.md). In short:

1. Build OpenWrt (pinned commit and feeds from [`firmware/`](firmware/)) and flash it.
2. Install your SSH key and apply the bridge config
   ([`firmware/README.md`](firmware/README.md) lists what to adapt to your network).
3. Install AdGuard Home and the transparent DNS redirect.
4. Build and install the app:
   ```sh
   bash <repo>/app/build.sh          # in WSL/Linux: go vet + tests + linux/arm64 binary
   scp -O app/bin/homework-hours root@<cm4-ip>:/usr/bin/homework-hours
   ```
5. Move the access point behind the CM4, then enable the DNS redirect with a self-reverting
   trial (`/etc/init.d/parentalcontrol trial 90`, then `confirm`).

## Usage

1. Open `https://<cm4-ip>:8443/login`, download `/ca.crt` once and trust it on each parent
   device, then sign in with your AdGuard Home account.
2. **Devices**: assign each child's devices to the child.
3. **Schedules**: add rules, for example "Weekdays: video and games open 18:00–21:30".
4. **Today**: see what is blocked for each child right now, pause the internet, or start an
   exception ("Homework done: video open for 1 hour").

AdGuard Home's own UI and LuCI listen on the CM4 only; open them through an SSH tunnel (see
[operations](.docs/OPERATIONS.md)).

## Limitations

- DNS filtering can be bypassed by cellular data, VPNs, or a browser's own "secure DNS"
  (DoH) set by hand; offline content isn't controlled. Changes take effect within about a minute.
- Blocking is per host name, so parts of a site (such as `youtube.com/edu`) can't be allowed alone.
- The school calendar sync is written for one district's feed; other iCalendar feeds may need
  small parser changes.

Planned: blocking known DoH resolvers, and trying [OpenAppFilter](https://github.com/destan19/OpenAppFilter)
(traffic-based app detection) as a second engine.

## Documentation

| Document | Contents |
|---|---|
| [.docs/STATUS.md](.docs/STATUS.md) | What is deployed and running right now |
| [.docs/DESIGN.md](.docs/DESIGN.md) | Requirements, topology, architecture, rule semantics, enforcement, security notes |
| [.docs/REBUILD.md](.docs/REBUILD.md) | Rebuild the whole CM4 from scratch, step by step |
| [.docs/OPERATIONS.md](.docs/OPERATIONS.md) | Access, build and deploy, testing, recovery |
| [.docs/DATA-AND-API.md](.docs/DATA-AND-API.md) | Settings file schema and the app's HTTP API |
| [.docs/DECISIONS.md](.docs/DECISIONS.md) | Decisions and the alternatives considered |
| [firmware/README.md](firmware/README.md) | What each firmware file is and what to change |
| [THIRD-PARTY.md](THIRD-PARTY.md) | Third-party components, their licenses, and what to do when distributing a device |
| [.docs/RESUME.md](.docs/RESUME.md), [CLAUDE.md](CLAUDE.md) | Picking the work up again (also for AI coding agents) |

## Repository layout

| Path | Contents |
|---|---|
| [`app/`](app/) | Homework Hours: Go (standard library only), web pages embedded from `app/web/`, tests; `build.sh` cross-compiles it |
| [`firmware/`](firmware/) | OpenWrt config (`diffconfig`, pinned feeds), package build, and the `files/` scripts that run on the CM4 |
| [`.docs/`](.docs/) | Design, operations, rebuild guide, API, decisions; `reference/` holds a redacted AdGuard config |

## Contributing

Questions, bug reports and ideas are welcome as issues. Because the project is dual-licensed
(see below), pull requests can only be merged under a contributor agreement that lets the
maintainer also offer your change under the commercial license; please open an issue first.
Before sending a change, run the tests: `bash app/build.sh` runs `go vet` and `go test`.

## License

Copyright © 2026 nankma.

Homework Hours is free software under the [GNU General Public License v3.0 or later](LICENSE):
you may use, study, modify and share it, including for your own home or for others, as long as
what you distribute stays under the GPL with its source available.

**Commercial options.** A license outside the GPL terms (for example for a closed product), a
ready-to-use device, and setup or installation services are available from the maintainer;
contact via GitHub.

Third-party components (OpenWrt, AdGuard Home, OpenAppFilter, Raspberry Pi firmware…) keep
their own licenses: see [THIRD-PARTY.md](THIRD-PARTY.md).
