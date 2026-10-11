package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	clientPrefix = "HH "
	ruleBegin    = "! homework-hours begin: managed by Homework Hours, edits here are overwritten"
	ruleEnd      = "! homework-hours end"
	nftTable     = "hh"
)

// Enforcer makes AdGuard Home and the bridge firewall match the rules: every minute, after
// every save, and at startup. It computes the desired state, compares it with what is there,
// and changes only the differences, so manual edits to our objects are reverted within a minute.
type Enforcer struct {
	store  *Store
	disc   *Discovery
	agh    *AdGuard
	loc    *time.Location
	bridge string
	kick   chan struct{}

	mu      sync.Mutex
	status  SyncStatus
	lastNft string
	ttlDone bool
}

// SyncStatus is what the page's sync panel shows.
type SyncStatus struct {
	Time            int64       `json:"time"` // ms; 0 = not run yet
	OK              bool        `json:"ok"`
	Errors          []string    `json:"errors"`
	AdGuard         bool        `json:"adguard"`
	DNSRedirect     bool        `json:"dnsRedirect"`
	WirelessDevices int         `json:"wirelessDevices"` // online devices behind the CM4
	Kids            []KidStatus `json:"kids"`
}

type KidStatus struct {
	ID              string   `json:"id"`
	Client          string   `json:"client"` // AdGuard client name, "" if none (no device IP known)
	IPs             []string `json:"ips"`
	Devices         int      `json:"devices"`
	NotBehind       int      `json:"notBehind"` // assigned devices not on the wireless side
	InternetBlocked bool     `json:"internetBlocked"`
	BlockedGroups   []string `json:"blockedGroups"`
	Services        []string `json:"services"`
	Sites           []string `json:"sites"`
	Adult           bool     `json:"adult"`
	SafeSearch      bool     `json:"safeSearch"`
}

func newEnforcer(store *Store, disc *Discovery, agh *AdGuard, loc *time.Location, bridge string) *Enforcer {
	return &Enforcer{store: store, disc: disc, agh: agh, loc: loc, bridge: bridge, kick: make(chan struct{}, 1)}
}

// trigger asks for a run soon (non-blocking; several kicks collapse into one run).
func (e *Enforcer) trigger() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Enforcer) Status() SyncStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	raw, _ := json.Marshal(e.status)
	var out SyncStatus
	_ = json.Unmarshal(raw, &out)
	return out
}

func (e *Enforcer) run(ctx context.Context) {
	e.reconcile()
	for {
		// Run just after each minute boundary, so "open at 5:00 PM" happens at 5:00:02
		now := time.Now()
		wait := now.Truncate(time.Minute).Add(time.Minute + 2*time.Second).Sub(now)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-e.kick:
			time.Sleep(300 * time.Millisecond) // let a burst of saves settle
		}
		e.reconcile()
	}
}

// clientName gives each child a readable AdGuard client name that is safe inside a
// $client='…' rule: letters, digits, spaces, '-' and '_' only, unique per child.
func clientNames(kids []Kid) map[string]string {
	names, used := map[string]string{}, map[string]bool{}
	for _, k := range kids {
		clean := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' {
				return r
			}
			return -1
		}, k.Name)), " ")
		if clean == "" {
			clean = k.ID
		}
		n := clientPrefix + clean
		if used[n] {
			n += "-" + k.ID
		}
		used[n] = true
		names[k.ID] = n
	}
	return names
}

func (e *Enforcer) reconcile() {
	now := time.Now().In(e.loc)
	st := e.store.Snapshot()
	plans := newEngine(st, now).Plan()
	names := clientNames(st.Kids)

	devs := e.disc.list(st.Devices)
	byMAC := map[string]DeviceInfo{}
	wireless := 0
	for _, d := range devs {
		byMAC[d.MAC] = d
		if d.Side == "wireless" && d.Online {
			wireless++
		}
	}

	status := SyncStatus{Time: now.UnixMilli(), WirelessDevices: wireless, Errors: []string{}}
	desired := map[string]Client{}
	var rules []string
	var offMACs []string
	for _, p := range plans {
		ks := KidStatus{ID: p.Kid.ID, IPs: []string{}, InternetBlocked: p.InternetBlocked,
			BlockedGroups: nonNil(p.BlockedGroups), Services: nonNil(p.Services), Sites: nonNil(p.Sites),
			Adult: p.Adult, SafeSearch: p.Kid.SafeSearch}
		for _, d := range st.Devices {
			if d.Kid != p.Kid.ID {
				continue
			}
			ks.Devices++
			info := byMAC[d.MAC]
			if info.Side != "wireless" {
				ks.NotBehind++
			}
			if info.IP != "" {
				ks.IPs = append(ks.IPs, info.IP)
			}
			if p.InternetBlocked {
				offMACs = append(offMACs, d.MAC)
			}
		}
		slices.Sort(ks.IPs)
		if len(ks.IPs) > 0 {
			name := names[p.Kid.ID]
			ks.Client = name
			desired[name] = desiredClient(name, ks.IPs, p, e.loc)
			for _, site := range p.Sites {
				rules = append(rules, fmt.Sprintf("||%s^$client='%s'", site, name))
			}
		}
		status.Kids = append(status.Kids, ks)
	}

	if err := e.applyNft(offMACs); err != nil {
		status.Errors = append(status.Errors, "Internet blocking: "+err.Error())
	}
	if errs := e.syncAdGuard(desired, rules); len(errs) > 0 {
		status.Errors = append(status.Errors, errs...)
	} else {
		status.AdGuard = true
	}
	if status.AdGuard && !e.ttlDone {
		if err := e.agh.ensureCacheTTLMax(60); err != nil {
			status.Errors = append(status.Errors, "AdGuard cache: "+err.Error())
		} else {
			e.ttlDone = true
		}
	}
	status.DNSRedirect = exec.Command("nft", "list", "table", "inet", "pc_dns").Run() == nil
	status.OK = len(status.Errors) == 0
	for _, msg := range status.Errors {
		log.Printf("enforce: %s", msg)
	}

	e.mu.Lock()
	e.status = status
	e.mu.Unlock()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func desiredClient(name string, ips []string, p KidPlan, loc *time.Location) Client {
	ss := p.Kid.SafeSearch
	return Client{
		Name: name, IDs: ips,
		UseGlobalSettings: false, FilteringEnabled: true, ParentalEnabled: p.Adult, SafebrowsingEnabled: false,
		SafeSearch:               SafeSearch{Enabled: ss, Bing: true, DuckDuckGo: true, Ecosia: true, Google: true, Pixabay: true, Yandex: true, YouTube: true},
		UseGlobalBlockedServices: false,
		// No days: AdGuard's own pause schedule is unused, blocked services apply all the time
		BlockedServicesSchedule: map[string]any{"time_zone": loc.String()},
		BlockedServices:         nonNil(p.Services),
		Upstreams:               []string{}, Tags: []string{},
	}
}

// sameClient compares the fields we manage, ignoring order and nil-vs-empty.
func sameClient(a, b Client) bool {
	norm := func(c Client) string {
		c.IDs = slices.Sorted(slices.Values(nonNil(c.IDs)))
		c.BlockedServices = slices.Sorted(slices.Values(nonNil(c.BlockedServices)))
		c.Upstreams, c.Tags = nonNil(c.Upstreams), nonNil(c.Tags)
		if c.BlockedServicesSchedule == nil {
			c.BlockedServicesSchedule = map[string]any{}
		}
		raw, _ := json.Marshal(c)
		return string(raw)
	}
	return norm(a) == norm(b)
}

func (e *Enforcer) syncAdGuard(desired map[string]Client, rules []string) []string {
	var errs []string
	existing, err := e.agh.clients()
	if err != nil {
		return []string{"AdGuard not reachable: " + err.Error()}
	}
	have := map[string]Client{}
	for _, c := range existing {
		have[c.Name] = c
	}
	// Delete first: a renamed child's old client still holds the device IPs
	for name := range have {
		if _, keep := desired[name]; strings.HasPrefix(name, clientPrefix) && !keep {
			if err := e.agh.deleteClient(name); err != nil {
				errs = append(errs, "remove "+name+": "+err.Error())
			} else {
				log.Printf("enforce: removed AdGuard client %s", name)
			}
		}
	}
	for name, c := range desired {
		old, ok := have[name]
		switch {
		case !ok:
			if err := e.agh.addClient(c); err != nil {
				errs = append(errs, "add "+name+": "+err.Error())
			} else {
				log.Printf("enforce: added AdGuard client %s %v", name, c.IDs)
			}
		case !sameClient(old, c):
			if err := e.agh.updateClient(name, c); err != nil {
				errs = append(errs, "update "+name+": "+err.Error())
			} else {
				log.Printf("enforce: updated %s: ips %v, services %v, adult %v", name, c.IDs, c.BlockedServices, c.ParentalEnabled)
			}
		}
	}

	cur, err := e.agh.userRules()
	if err != nil {
		return append(errs, "AdGuard rules: "+err.Error())
	}
	if next := mergeRules(cur, rules); !slices.Equal(cur, next) {
		if err := e.agh.setUserRules(next); err != nil {
			errs = append(errs, "AdGuard rules: "+err.Error())
		} else {
			log.Printf("enforce: %d site rules", len(rules))
		}
	}
	return errs
}

// mergeRules replaces our marked block in the user rules and keeps every other rule as is.
func mergeRules(cur, ours []string) []string {
	out := []string{}
	in := false
	for _, r := range cur {
		switch {
		case r == ruleBegin:
			in = true
		case r == ruleEnd:
			in = false
		case !in:
			out = append(out, r)
		}
	}
	if len(ours) > 0 {
		out = append(out, ruleBegin)
		out = append(out, ours...)
		out = append(out, ruleEnd)
	}
	return out
}

// renderNft builds the bridge table that cuts a child's devices off the internet: frames from
// their MACs to addresses outside the home network are dropped (open connections and apps with
// hard-coded IPs included). DHCP, ARP and traffic to home devices still pass.
func renderNft(macs, lan4 []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "add table bridge %s\ndelete table bridge %s\ntable bridge %s {\n", nftTable, nftTable, nftTable)
	b.WriteString("\tset off {\n\t\ttype ether_addr\n")
	if len(macs) > 0 {
		fmt.Fprintf(&b, "\t\telements = { %s }\n", strings.Join(macs, ", "))
	}
	b.WriteString("\t}\n\tchain cut {\n\t\ttype filter hook forward priority 0; policy accept;\n")
	b.WriteString("\t\tether saddr != @off accept\n")
	b.WriteString("\t\tether type arp accept\n")
	b.WriteString("\t\tudp dport 67 accept\n")
	local := append([]string{"255.255.255.255", "224.0.0.0/4"}, lan4...)
	fmt.Fprintf(&b, "\t\tip daddr { %s } accept\n", strings.Join(local, ", "))
	b.WriteString("\t\tip6 daddr { fe80::/10, ff00::/8 } accept\n")
	b.WriteString("\t\tether type { ip, ip6 } drop\n\t}\n}\n")
	return b.String()
}

func (e *Enforcer) lanNets() []string {
	var nets []string
	ifc, err := net.InterfaceByName(e.bridge)
	if err != nil {
		return nets
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			ones, _ := n.Mask.Size()
			nets = append(nets, fmt.Sprintf("%s/%d", n.IP.Mask(n.Mask), ones))
		}
	}
	slices.Sort(nets)
	return nets
}

func (e *Enforcer) applyNft(macs []string) error {
	slices.Sort(macs)
	macs = slices.Compact(macs)
	text := renderNft(macs, e.lanNets())
	e.mu.Lock()
	same := text == e.lastNft
	e.mu.Unlock()
	if same {
		return nil
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("enforce: internet off for %d device(s)", len(macs))
	e.mu.Lock()
	e.lastNft = text
	e.mu.Unlock()
	return nil
}
