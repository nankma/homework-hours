package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Seen is what the network tells us about one MAC address. It is runtime data, kept apart
// from state.json so that minute-by-minute updates never bump the settings version.
type Seen struct {
	IP       string    `json:"ip,omitempty"`
	Port     string    `json:"port,omitempty"` // bridge port the MAC was last learned on
	LastSeen time.Time `json:"lastSeen"`
	// FirstSeen is when discovery first met the MAC; zero for devices known before this field
	// existed (2026-10-10), which therefore never count as new.
	FirstSeen time.Time `json:"firstSeen,omitempty"`
	Host      string    `json:"host,omitempty"` // reverse DNS name from the router (DHCP host name)
	hostTry   time.Time
	hostIP    string
}

// Discovery polls the bridge forwarding table and the neighbor table every minute.
type Discovery struct {
	mu       sync.Mutex
	path     string
	bridge   string
	wireless string // bridge port facing the wireless hub
	gateway  string // router, answers reverse DNS for DHCP clients
	seen     map[string]*Seen
	dirty    bool
}

const (
	pollEvery   = time.Minute
	sweepEvery  = 5 * time.Minute
	saveEvery   = 15 * time.Minute
	forgetAfter = 30 * 24 * time.Hour
	onlineFor   = 10 * time.Minute
)

func openDiscovery(path, bridge, wireless, gateway string) *Discovery {
	d := &Discovery{path: path, bridge: bridge, wireless: wireless, gateway: gateway, seen: map[string]*Seen{}}
	raw, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(raw, &d.seen) != nil {
			d.seen = map[string]*Seen{} // only history is lost
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("discovery: %v", err)
	}
	return d
}

// run polls until ctx ends. keep reports MACs that must never be forgotten (stored devices).
func (d *Discovery) run(ctx context.Context, keep func(mac string) bool) {
	d.sweep()
	d.poll()
	poll, sweep, save := time.NewTicker(pollEvery), time.NewTicker(sweepEvery), time.NewTicker(saveEvery)
	defer poll.Stop()
	defer sweep.Stop()
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			d.poll()
		case <-sweep.C:
			d.sweep()
		case <-save.C:
			d.save(keep)
		}
	}
}

type fdbEntry struct {
	MAC    string   `json:"mac"`
	Ifname string   `json:"ifname"`
	Master string   `json:"master"`
	State  string   `json:"state"`
	Flags  []string `json:"flags"`
}

// readFDB returns MAC → bridge port for MACs learned on the bridge's ports (not the CM4's own).
func readFDB(bridge string) (map[string]string, error) {
	out, err := exec.Command("bridge", "-j", "fdb", "show", "br", bridge).Output()
	if err != nil {
		return nil, err
	}
	var entries []fdbEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, e := range entries {
		if e.State == "permanent" || e.Master != bridge || isMulticastMAC(e.MAC) {
			continue
		}
		m[strings.ToLower(e.MAC)] = e.Ifname
	}
	return m, nil
}

// readARP returns IP → MAC for complete IPv4 neighbor entries on the bridge.
func readARP(bridge string) map[string]string {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		// IP, HW type, Flags, HW address, Mask, Device
		if len(fl) < 6 || fl[5] != bridge || fl[2] == "0x0" || fl[3] == "00:00:00:00:00:00" {
			continue
		}
		m[fl[0]] = strings.ToLower(fl[3])
	}
	return m
}

func isMulticastMAC(mac string) bool {
	hw, err := net.ParseMAC(mac)
	return err != nil || len(hw) != 6 || hw[0]&1 == 1
}

// privateMAC reports a locally administered address, which phones use for "private Wi-Fi address".
func privateMAC(mac string) bool {
	hw, err := net.ParseMAC(mac)
	return err == nil && len(hw) == 6 && hw[0]&2 == 2
}

func (d *Discovery) poll() {
	fdb, err := readFDB(d.bridge)
	if err != nil {
		log.Printf("discovery: bridge fdb: %v", err)
		return
	}
	arp := readARP(d.bridge)
	now := time.Now()

	d.mu.Lock()
	for mac, port := range fdb {
		s := d.seen[mac]
		if s == nil {
			s = &Seen{FirstSeen: now}
			d.seen[mac] = s
			if port == d.wireless {
				log.Printf("discovery: new device %s behind the CM4", mac)
			}
		}
		s.Port, s.LastSeen = port, now
	}
	for ip, mac := range arp {
		s := d.seen[mac]
		if s == nil || s.IP == ip {
			continue
		}
		for _, other := range d.seen { // an IP belongs to one device at a time
			if other.IP == ip {
				other.IP = ""
			}
		}
		s.IP = ip
	}
	d.dirty = true
	var lookups []string
	for mac, s := range d.seen {
		if s.IP != "" && (s.hostIP != s.IP || (s.Host == "" && now.Sub(s.hostTry) > time.Hour)) {
			s.hostTry, s.hostIP = now, s.IP
			lookups = append(lookups, mac+" "+s.IP)
		}
	}
	d.mu.Unlock()

	for _, l := range lookups {
		mac, ip, _ := strings.Cut(l, " ")
		host := d.reverseName(ip)
		d.mu.Lock()
		if s := d.seen[mac]; s != nil && s.IP == ip && host != "" {
			s.Host = host
		}
		d.mu.Unlock()
	}
}

// reverseName asks the router for the DHCP host name of ip.
func (d *Discovery) reverseName(ip string) string {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dl net.Dialer
		return dl.DialContext(ctx, "udp", net.JoinHostPort(d.gateway, "53"))
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	names, err := r.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

// sweep sends one tiny UDP packet to every address of the bridge's /24 (or smaller) networks.
// The kernel resolves each address first, which fills the neighbor table with the IPs of
// devices the CM4 itself never talks to.
func (d *Discovery) sweep() {
	ifc, err := net.InterfaceByName(d.bridge)
	if err != nil {
		return
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil {
			continue
		}
		if ones, _ := n.Mask.Size(); ones < 24 {
			continue
		}
		base := n.IP.To4().Mask(n.Mask)
		for i := 1; i < 255; i++ {
			ip := net.IPv4(base[0], base[1], base[2], byte(i))
			if !n.Contains(ip) || ip.Equal(n.IP) {
				continue
			}
			if c, err := net.Dial("udp4", net.JoinHostPort(ip.String(), "9")); err == nil {
				_, _ = c.Write([]byte{0})
				c.Close()
			}
		}
	}
}

func (d *Discovery) save(keep func(string) bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.dirty {
		return
	}
	for mac, s := range d.seen {
		if time.Since(s.LastSeen) > forgetAfter && !keep(mac) {
			delete(d.seen, mac)
		}
	}
	raw, _ := json.Marshal(d.seen)
	if err := writeFileAtomic(d.path, raw, 0o600); err != nil {
		log.Printf("discovery: save: %v", err)
		return
	}
	d.dirty = false
}

func (d *Discovery) known(mac string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.seen[mac]
	return ok
}

func (d *Discovery) ipOf(mac string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.seen[mac]; s != nil {
		return s.IP
	}
	return ""
}

// DeviceInfo is one row of the Devices tab: network facts plus the parent's name and child.
type DeviceInfo struct {
	MAC       string `json:"mac"`
	Name      string `json:"name"`
	Kid       string `json:"kid"`
	Host      string `json:"host"`
	IP        string `json:"ip"`
	Side      string `json:"side"`      // "wireless" (behind the CM4), "other", or "" if never seen
	LastSeen  int64  `json:"lastSeen"`  // ms since epoch, 0 = never
	FirstSeen int64  `json:"firstSeen"` // ms since epoch, 0 = before 2026-10-10 or never
	Reviewed  bool   `json:"reviewed"`  // parent marked it "not a child's device"
	Online    bool   `json:"online"`
	Private   bool   `json:"private"`
}

func (d *Discovery) list(stored []Device) []DeviceInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	byMAC := map[string]*DeviceInfo{}
	for mac, s := range d.seen {
		side := "other"
		if s.Port == d.wireless {
			side = "wireless"
		}
		byMAC[mac] = &DeviceInfo{MAC: mac, Host: s.Host, IP: s.IP, Side: side,
			LastSeen: s.LastSeen.UnixMilli(), Online: time.Since(s.LastSeen) < onlineFor}
		if !s.FirstSeen.IsZero() {
			byMAC[mac].FirstSeen = s.FirstSeen.UnixMilli()
		}
	}
	for _, sd := range stored {
		di := byMAC[sd.MAC]
		if di == nil {
			di = &DeviceInfo{MAC: sd.MAC}
			byMAC[sd.MAC] = di
		}
		di.Name, di.Kid, di.Reviewed = sd.Name, sd.Kid, sd.Reviewed
	}
	out := make([]DeviceInfo, 0, len(byMAC))
	for _, di := range byMAC {
		di.Private = privateMAC(di.MAC)
		out = append(out, *di)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].MAC < out[b].MAC })
	return out
}
