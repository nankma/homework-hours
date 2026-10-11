# Data model and HTTP API

## state.json (`/etc/homework-hours/state.json`)

Whole configuration in one versioned JSON document. Go types: `app/store.go`; checks:
`app/validate.go` (the page's shapes are the same).

```jsonc
{
  "version": 7,                       // +1 on every save; clients send the version they edited
  "kids": [{
    "id": "k1abc", "name": "Maya", "color": "#3f7d6e",   // color: #rrggbb
    "safeSearch": true, "paused": false,
    "rules": [{                        // ordered; first match wins per site group
      "id": "r1", "name": "Video after school",
      "dates": ["any"],                // OR of: "any" | "kind:holiday" | "kind:break" | "kind:custom" | "entry:<calendar id>"
      "sites": ["video"],              // OR of: "internet" | group ids
      "days": {                        // weekday "0"=Sun … "6"=Sat; missing = rule doesn't apply that day
        "1": [[1020, 1200]],           // open windows in minutes [from, to), sorted, non-overlapping, 0..1440
        "0": []                        // [] = blocked all day
      }
    }]
  }],
  "groups": [{
    "id": "video", "name": "Video & social",
    "members": [
      {"kind": "service", "id": "youtube"},          // AdGuard blocked-service id
      {"kind": "site", "id": "pornhub.com"},         // domain, covers subdomains
      {"kind": "filter", "id": "adult", "label": "Adult content filter"}  // category (only "adult")
    ]
  }],
  "devices": [{"mac": "a4:83:e7:12:9c:01", "name": "Maya's iPad", "kid": "k1abc"},  // kid "" = not managed
              {"mac": "02:11:22:33:44:55", "name": "", "kid": "", "reviewed": true}],  // "not a child's device"
  "calendar": [{"id": "m1", "name": "Summer break", "from": "2027-06-25", "to": "2027-09-07",
                "kind": "break", "source": "manual"}],                 // kind: holiday | break | custom
  "district": {
    "lastSync": "2026-10-10T05:10:31Z",     // server-owned: last successful sync
    "lastAttempt": "2026-10-10T05:10:31Z",  // server-owned: last try
    "lastError": "",                        // server-owned: why the last try failed
    "feed": [{"date": "2026-12-21", "to": "2027-01-01", "note": "No school, Winter Break"}],  // server-owned
    "notices": [{"at": "…", "text": "Changed: Winter Break is now …"}],  // server-owned, until dismissed
    "changed": {"d2026-12-21": {"to": "2027-01-04"}},  // parent: new last day of a district entry
    "ignored": {"d2027-03-19": true}
  },
  "exceptions": [{"id": "x…", "kid": "k1abc", "sites": ["video"], "open": true,
                  "from": 1791600000000, "until": 1791603600000, "note": "Homework done"}],  // ms since epoch
  "favorites": [{"id": "f…", "kid": "k1abc", "sites": ["video"], "open": true, "minutes": 60, "note": ""}]
}
```

Server-side rules on save (`validateState` + `Store.Replace`):
- Ids `^[A-Za-z0-9_-]{1,40}$`, unique per list; `internet` is reserved. Names trimmed, control
  characters removed, length-capped.
- Rule/exception/favorite sites must name existing groups or `internet`; rules need ≥1 date,
  site and weekday; windows valid and non-overlapping (sorted on save).
- Domains must look like `example.com` (lowercased). Service ids `^[a-z0-9_]{1,40}$`.
- Exceptions: at most 12 h 5 min long; expired ones are dropped; only the newest per child kept.
- Favorite minutes ∈ {30, 60, 120, 180, 240, 360, 720}.
- `devices`: the page's list replaces the stored one, but only for MACs that discovery has seen
  or that were already stored; only entries with a name, a child or `reviewed` are kept. Kids that no
  longer exist are unassigned.
- `district.lastSync`, `lastAttempt`, `lastError`, `feed` and `notices` are kept from the server.
- Limits: 20 kids, 50 groups, 500 members/group, 50 rules/kid, 500 calendar entries, 100
  favorites, body ≤ 1 MiB.

District entries are derived from `district.feed` by the same expansion in Go
(`districtBase`, `app/district.go`) and in the page; ids are `d<first date>`. The feed is
refreshed from the district ICS (DESIGN §4b); a sync bumps `version`.

## HTTP API (`https://192.168.0.2:8443`)

All JSON. Errors are `{"error": "<message for parents>"}`.

| Method & path | Auth | Purpose |
|---|---|---|
| `GET /` | session, else 303 → `/login` | The app page |
| `GET /login` | none | Sign-in page |
| `GET /ca.crt` | none | Home CA certificate download |
| `POST /api/login` `{"name","password"}` | none | Checks against AdGuard; sets `hh_session`. 401 wrong, 429 locked (5 fails / 15 min per IP), 502 AdGuard down. Service account refused |
| `POST /api/logout` | session + `X-HH: 1` | Ends the session |
| `GET /api/me` | session | `{"user": "admin"}` |
| `GET /api/state` | session | The state document |
| `PUT /api/state` (state with the loaded `version`) | session + `X-HH: 1` | Save. 200 → saved state (new version); 409 → `{"error", "state": current}`; 400 → validation message |
| `GET /api/devices` | session | `{"devices": [{mac, name, kid, host, ip, side: "wireless"\|"other"\|"", lastSeen (ms, 0 = never), firstSeen (ms, 0 = known before 2026-10-10), reviewed, online, private}]}`: discovered devices merged with stored ones |
| `GET /api/activity?mac=&range=` | session | range ∈ 30, 60, 120, 240, 1440, 10080 minutes → `{"ip", "truncated", "domains": [{domain, n, last (ms), blocked}]}` |
| `GET /api/catalog` | session | `{"services": [{id, name, group, domains}]}` from AdGuard, cached 24 h |
| `POST /api/district/sync` | session + X-HH | Fetch the district calendar now: `{state, error?}` (200 with the old feed and `error` when the fetch fails) |
| `POST /api/district/dismiss` | session + X-HH | Clear `district.notices`; returns the state |
| `GET /api/sync` | session | Last enforcement run: `{time, ok, errors[], adguard, dnsRedirect, wirelessDevices, kids: [{id, client, ips, devices, notBehind, internetBlocked, blockedGroups, services, sites, adult, safeSearch}]}` |

Without a session: 401 `{"error":"Please sign in again."}`. Non-GET without `X-HH: 1`: 403.


## AdGuard Home API used

Base `http://127.0.0.1:3000`, Basic auth with the `pcapi` account (`agh.secret`).

| Call | Use |
|---|---|
| `GET /control/status` | Login check (with the parent's credentials) |
| `GET /control/clients`, `POST /control/clients/add`, `/update` (`{"name", "data"}`), `/delete` (`{"name"}`) | `HH <child>` clients |
| `GET /control/filtering/status`, `POST /control/filtering/set_rules` (`{"rules": [...]}`) | Our user-rule block |
| `GET /control/dns_info`, `POST /control/dns_config` (`{"cache_ttl_max": 60}`) | Cache cap, set once |
| `GET /control/filtering/check_host?name=&client=<ip>` | Testing: what AdGuard would do for that client |
| `GET /control/blocked_services/all` | Service catalog and domains |
| `GET /control/querylog?search="<ip>"&limit=500&older_than=<oldest>` | Device activity (quoted = strict match; unquoted is a substring search) |

Per-client settings are ignored unless `use_global_settings` is false (found while testing).
