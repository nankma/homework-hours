# Third-party software

Homework Hours itself (this repository) is licensed under GPL-3.0-or-later (see
[LICENSE](LICENSE)). A finished CM4 also runs the third-party software below. None of it is
copied into this repository: the firmware is built from the upstream sources pinned in
[`firmware/feeds.conf.pinned`](firmware/feeds.conf.pinned) and
[`firmware/diffconfig`](firmware/diffconfig), and the app talks to AdGuard Home only through
its HTTP API.

| Component | License | Source |
|---|---|---|
| OpenWrt (base system, commit `e9a6e45556e66067f7359509a7866cdbb8fe3f29`) | GPL-2.0-only; individual packages under their own licenses (GPL, LGPL, BSD, MIT, ISC…) | https://git.openwrt.org/openwrt/openwrt.git |
| OpenWrt package feeds (packages, luci, routing, telephony, video) | Per package; LuCI is Apache-2.0 | commits in `firmware/feeds.conf.pinned` |
| AdGuard Home 0.107.78 (OpenWrt package) | GPL-3.0 | https://github.com/AdguardTeam/AdGuardHome |
| OpenAppFilter (built, not installed) | GPL-2.0 | https://github.com/destan19/OpenAppFilter (commit in `firmware/feeds.conf.pinned`) |
| Raspberry Pi boot firmware (Broadcom binaries in the OpenWrt image) | Redistributable in binary form for Raspberry Pi devices only; notice must be reproduced | https://github.com/raspberrypi/firmware (`boot/LICENCE.broadcom`) |
| Go standard library (compiled into the app binary) | BSD-3-Clause | https://go.dev |
| AdGuard Home filter lists (downloaded at run time) | Each list's own license (e.g. AdGuard DNS filter, AdAway Default Blocklist) | links in `.docs/reference/adguardhome.yaml` |

Product names (OpenWrt, AdGuard, Raspberry Pi, Waveshare) are trademarks of their owners and
are used here only to describe compatibility; no endorsement is implied.

## When you distribute a device or an installed system

Shipping a CM4 with this software, or leaving it installed on someone else's hardware, is
distribution under the GPL. Then:

1. Provide the complete corresponding source of the GPL components (OpenWrt with the pinned
   feeds and build configuration, AdGuard Home, OpenAppFilter if installed, and this app), or a
   written offer valid for at least three years to provide it.
2. Include the license texts and copyright notices (GPL-2.0, GPL-3.0, Apache-2.0, BSD, the
   Broadcom firmware notice), e.g. in the printed manual or on a page of the device.
3. Don't lock the device: owners must be able to install modified versions (GPL-3.0
   "Installation Information" for consumer products). Root SSH access satisfies this.
4. Publish your changes if you modify any GPL component.
