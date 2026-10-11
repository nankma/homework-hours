# Homework Hours: parental control on the CM4 bridge

A Waveshare CM4-DUAL-ETH-BASE running OpenWrt sits between the home router and the wireless
hub as a transparent bridge. It sends every wireless device's DNS to AdGuard Home and runs
**Homework Hours**, our own web app, where parents set per-child, time-based rules per site
group (e.g. "YouTube blocked on school days before 5 PM, open early when homework is done").
Wired servers are not controlled.

```
ISP fiber → Q1000K (router: NAT + DHCP, 192.168.0.1)
              ├─ 10G LAN → wired servers                      (not controlled)
              └─ 1G LAN  → CM4 eth1 ═ br-lan (192.168.0.2) ═ CM4 eth0
                                                                └→ wireless hub → all user devices (controlled)
```

## Where things stand (2026-10-10)

Live: the hub is behind the CM4, wireless DNS goes through AdGuard, and the app enforces the
rules (steps 1–5: HTTPS + login, settings, device discovery, enforcement, school calendar sync).
Details: [STATUS.md](STATUS.md).

## Documents

| File | What's in it |
|---|---|
| [DESIGN.md](DESIGN.md) | Requirements, network topology, architecture, rule semantics, how settings become AdGuard/nft config, security, known limits |
| [DATA-AND-API.md](DATA-AND-API.md) | `state.json` schema and the HTTP API of the app |
| [REBUILD.md](REBUILD.md) | Rebuild this exact CM4 from scratch: build host, sources and pinned feeds, firmware, flashing, bridge config, packages, AdGuard, DNS redirect, app, go-live |
| [OPERATIONS.md](OPERATIONS.md) | Access, build, deploy, test, recover; files on the CM4 |
| [STATUS.md](STATUS.md) | Exactly what is deployed and running right now |
| [DECISIONS.md](DECISIONS.md) | Decision log with reasons and rejected alternatives |
| [RESUME.md](RESUME.md) | Checklist for picking the work up in a new session |

Also: `../CLAUDE.md` (agent instructions: access, package build workflow, network invariants)
and [`../firmware/README.md`](../firmware/README.md) (what each firmware file is, what to change).

## Quick links

- Parent page: `https://192.168.0.2:8443` (install `/ca.crt` once per parent device)
- AdGuard Home and LuCI: local to the CM4 only; open through the SSH tunnel in OPERATIONS.md
  (`http://localhost:3000`, `http://localhost:8080`)
- SSH: `ssh -i ~/.ssh/cm4pi_rsa root@192.168.0.2`

## Repo layout

| Path | Contents |
|---|---|
| `app/` | Homework Hours (Go, stdlib only). `build.sh` cross-compiles it in WSL |
| `app/web/` | Embedded pages: `index.html` (the app), `login.html` |
| `firmware/` | `diffconfig` + `feeds.conf.pinned` (exact firmware config), package build (`config.seed`, `build.sh`) and the `files/` overlay (init scripts, UCI defaults, nft templates); see `firmware/README.md` |
| `.docs/reference/` | Redacted copy of the live AdGuard config |
