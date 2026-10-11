# Operations

## Access

| What | How |
|---|---|
| SSH | `ssh -i ~/.ssh/cm4pi_rsa root@192.168.0.2` (key only; use the RSA key, Windows OpenSSH fails with ed25519 against this dropbear) |
| Parent page | `https://192.168.0.2:8443` |
| AdGuard Home | Local only: SSH tunnel (below), then `http://localhost:3000` (accounts `admin`, `pcapi`) |
| LuCI | Local only: SSH tunnel, then `http://localhost:8080` (an unauthenticated LuCI answers 403 with the login form; that is normal) |
| SSH tunnel | `ssh -i ~/.ssh/cm4pi_rsa -L 3000:127.0.0.1:3000 -L 8080:127.0.0.1:80 root@192.168.0.2` (keep it open while you use the pages) |
| Service cable fallback | PC with static `192.168.0.250/24`, no gateway, cable to CM4 **eth0**, then `root@192.168.0.2` |

BusyBox userland: no `nohup`, `od` or `ip -br`. To survive a network reload over SSH:
`sh -c "sleep 1; /etc/init.d/network reload" </dev/null >/tmp/x.log 2>&1 &`.

## Files on the CM4

| Path | What |
|---|---|
| `/usr/bin/homework-hours` | The app |
| `/etc/init.d/homework-hours` | procd service (enabled) |
| `/etc/homework-hours/` | `state.json`, `sessions.json`, `seen.json` (device network facts), `ca.crt/key`, `server.crt/key` (mode 600/700) |
| `/etc/parentalcontrol/agh.secret` | `pcapi:<password>` for the AdGuard API |
| `/etc/config/parentalcontrol` | DNS redirect settings (`dns.enabled`, `port`, `only_mac`) |
| `/etc/init.d/parentalcontrol` | Loads the DNS redirect nft tables (`start`, `stop`, `trial [sec]`, `confirm`); enabled at boot |
| `/usr/share/parentalcontrol/dns-redirect.nft.in` | nft template |
| `/etc/adguardhome/adguardhome.yaml` | AdGuard config (query log rotates every 7 d → ≤14 d kept; stats 14 d) |
| `/mnt/data` | Data partition (`/dev/mmcblk0p3`, ext4, 14 GB), mounted by `/etc/init.d/datafs` |
| `/mnt/data/adguardhome` | AdGuard work dir (UCI `adguardhome.config.work_dir`): query log, statistics, filters |
| `/etc/sysupgrade.conf` | Keeps `/etc/homework-hours`, `/etc/parentalcontrol`, our init scripts and rc.d links, `/usr/bin/homework-hours`, `/usr/share/parentalcontrol` |

Logs: `logread -e homework-hours`, `logread -e AdGuardHome`, `logread -e parentalcontrol`.
Note the app logs in UTC (no zoneinfo; see DESIGN §4 time zone).

## Build and deploy the app

```sh
# Build in WSL as the build user (vet + tests + arm64 binary → app/bin/homework-hours)
bash <repo>/app/build.sh

# Deploy
K="-i $HOME/.ssh/cm4pi_rsa"
scp -O $K app/bin/homework-hours root@192.168.0.2:/tmp/hh.new
ssh $K root@192.168.0.2 '/etc/init.d/homework-hours stop; mv /tmp/hh.new /usr/bin/homework-hours; chmod 755 /usr/bin/homework-hours; /etc/init.d/homework-hours start'
```

- Pages under `app/web/` are embedded at build time; a page change needs a rebuild + deploy.
- `gofmt`: `~/openwrt/openwrt/staging_dir/hostpkg/lib/go-1.26/bin/gofmt -l .` in WSL.
- JS syntax check of the page: extract the `<script>` block and run `node --check`. A stub-DOM
  harness that runs the page against fake API answers caught a real bug once; worth repeating
  for bigger page changes.
- Copy the init script from `firmware/files/etc/init.d/homework-hours` if it changes.

## Build extra OpenWrt packages (no reflash)

See `../CLAUDE.md` → "Building and installing extra packages". Short version: add `=m` to
`firmware/config.seed`, run `firmware/build.sh` in WSL, `apk add --no-network` the `.apk`
files on the CM4. Never add anything that changes the kernel config (vermagic must stay
`df0c3e7257d9a8cf652064c77493e840`).

## Testing

### API without knowing the parent's password
Inject a temporary session, test, then log it out (removes it):

```sh
D=/etc/homework-hours; T=selftest$(head -c8 /dev/urandom | hexdump -e '"%02x"')
sed -i "s/^{/{\"$T\":{\"user\":\"selftest\",\"expires\":\"<RFC3339 a few hours ahead>\"},/" $D/sessions.json
/etc/init.d/homework-hours restart
curl -s --cacert $D/ca.crt -b hh_session=$T https://192.168.0.2:8443/api/state
curl -s --cacert $D/ca.crt -b hh_session=$T -X POST -H 'X-HH: 1' https://192.168.0.2:8443/api/logout
```

(If `sessions.json` is `{}` or missing, write the whole object instead.) Run curl **on the CM4**:
Git Bash's curl can't use `--cacert` here. If you trigger the login lockout while testing,
restart the service to clear it.

### Enforcement with a real device
Assign a test device to a child (Devices tab or `PUT /api/state` with a test session), add a
temporary rule or exception, then from that device: `nslookup www.youtube.com 192.168.0.1`
(blocked → `0.0.0.0`; the query is redirected to AdGuard whatever server you ask). On the CM4,
`curl -u "$(cat /etc/parentalcontrol/agh.secret)" 'http://127.0.0.1:3000/control/filtering/check_host?name=www.youtube.com&client=<device ip>'`
shows AdGuard's reason. Windows caches a blocked answer ~10 s (`ipconfig /flushdns`). Remove the
test rule, exception and assignment afterwards.

Other checks: `google.com` → `216.239.38.120` with safe search on, `use-application-dns.net` →
NXDOMAIN.

## Recovery

- **CM4 down / network broken:** plug the wireless hub directly into the Q1000K 1G port.
- **DNS redirect suspected:** `/etc/init.d/parentalcontrol stop` removes both nft tables.
  To test changes safely: `/etc/init.d/parentalcontrol trial 90`, then `confirm`.
- **App won't start:** `logread -e homework-hours`. A corrupt `state.json` stops startup; restore
  from a backup or move it away (a fresh default state is created). A corrupt `sessions.json`
  only signs everyone out.
- **Config backup:** `sysupgrade -b /tmp/backup.tar.gz`, then copy it off the CM4 and keep it private (it holds the AdGuard service password and the TLS keys).
- **Data partition missing** (`logread -e datafs` says tmpfs): re-create the entry with
  `fdisk /dev/mmcblk0` → `n`, `p`, `3`, start `360448`, end default, `w` (do **not** format),
  then reboot or `/etc/init.d/adguardhome stop; umount /mnt/data; /etc/init.d/datafs start;
  /etc/init.d/adguardhome start`.
- **Certificate:** delete `server.crt/key` to re-issue the leaf; deleting `ca.*` means every
  parent device must install the new CA.
