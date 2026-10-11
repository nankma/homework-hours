package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// AdGuard talks to the local AdGuard Home API. Parents sign in with their AdGuard accounts;
// the service account (from the secret file) is used for everything the program does itself.
type AdGuard struct {
	base        string
	serviceUser string
	servicePass string
	http        *http.Client
}

func newAdGuard(base, secretFile string) (*AdGuard, error) {
	raw, err := os.ReadFile(secretFile)
	if err != nil {
		return nil, err
	}
	user, pass, ok := strings.Cut(strings.TrimSpace(string(raw)), ":")
	if !ok || user == "" || pass == "" {
		return nil, fmt.Errorf("%s must contain user:password", secretFile)
	}
	return &AdGuard{base: strings.TrimRight(base, "/"), serviceUser: user, servicePass: pass,
		http: &http.Client{Timeout: 10 * time.Second}}, nil
}

// checkLogin reports whether name/password is a valid AdGuard Home account.
func (a *AdGuard) checkLogin(name, password string) (bool, error) {
	req, err := http.NewRequest(http.MethodGet, a.base+"/control/status", nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(name, password)
	resp, err := a.http.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, nil
	default:
		return false, fmt.Errorf("adguard answered %s", resp.Status)
	}
}

// get calls a read-only API endpoint as the service account and decodes the JSON answer.
func (a *AdGuard) get(path string, q url.Values, out any) error {
	u := a.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(a.serviceUser, a.servicePass)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("adguard %s answered %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Service is one entry of AdGuard's blocked-services catalog.
type Service struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Group   string   `json:"group"`
	Domains []string `json:"domains"`
}

// services returns the catalog with each service's domains (from its "||domain^" rules).
func (a *AdGuard) services() ([]Service, error) {
	var in struct {
		BlockedServices []struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Rules   []string `json:"rules"`
			GroupID string   `json:"group_id"`
		} `json:"blocked_services"`
	}
	if err := a.get("/control/blocked_services/all", nil, &in); err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(in.BlockedServices))
	for _, s := range in.BlockedServices {
		svc := Service{ID: s.ID, Name: s.Name, Group: s.GroupID, Domains: []string{}}
		for _, r := range s.Rules {
			d, ok := strings.CutPrefix(r, "||")
			if d, ok2 := strings.CutSuffix(d, "^"); ok && ok2 && reDomain.MatchString(d) {
				svc.Domains = append(svc.Domains, d)
			}
		}
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// DomainHits sums one device's lookups of one host name.
type DomainHits struct {
	Domain  string `json:"domain"`
	Count   int    `json:"n"`
	Last    int64  `json:"last"`    // ms since epoch
	Blocked int    `json:"blocked"` // lookups AdGuard filtered
}

// activity reads the query log of one client IP back to since, at most maxEntries entries.
// truncated is true when the limit cut the range short.
func (a *AdGuard) activity(ip string, since time.Time, maxEntries int) (hits []DomainHits, truncated bool, err error) {
	byDomain := map[string]*DomainHits{}
	olderThan, seen := "", 0
	for seen < maxEntries {
		q := url.Values{"search": {`"` + ip + `"`}, "limit": {"500"}}
		if olderThan != "" {
			q.Set("older_than", olderThan)
		}
		var page struct {
			Data []struct {
				Client   string    `json:"client"`
				Time     time.Time `json:"time"`
				Reason   string    `json:"reason"`
				Question struct {
					Name string `json:"name"`
				} `json:"question"`
			} `json:"data"`
			Oldest string `json:"oldest"`
		}
		if err := a.get("/control/querylog", q, &page); err != nil {
			return nil, false, err
		}
		done := len(page.Data) == 0 || page.Oldest == ""
		for _, e := range page.Data {
			if e.Time.Before(since) {
				done = true
				break
			}
			seen++
			if e.Client != ip {
				continue
			}
			name := strings.TrimSuffix(strings.ToLower(e.Question.Name), ".")
			h := byDomain[name]
			if h == nil {
				h = &DomainHits{Domain: name}
				byDomain[name] = h
			}
			h.Count++
			if ms := e.Time.UnixMilli(); ms > h.Last {
				h.Last = ms
			}
			if strings.HasPrefix(e.Reason, "Filtered") && e.Reason != "FilteredSafeSearch" {
				h.Blocked++
			}
		}
		if done {
			break
		}
		olderThan = page.Oldest
		truncated = seen >= maxEntries
	}
	hits = make([]DomainHits, 0, len(byDomain))
	for _, h := range byDomain {
		hits = append(hits, *h)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Count > hits[j].Count })
	return hits, truncated, nil
}

// post sends a JSON body to a write endpoint as the service account.
func (a *AdGuard) post(path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, a.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.SetBasicAuth(a.serviceUser, a.servicePass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("adguard %s answered %s: %s", path, resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

// SafeSearch is AdGuard's per-client safe search block.
type SafeSearch struct {
	Enabled    bool `json:"enabled"`
	Bing       bool `json:"bing"`
	DuckDuckGo bool `json:"duckduckgo"`
	Ecosia     bool `json:"ecosia"`
	Google     bool `json:"google"`
	Pixabay    bool `json:"pixabay"`
	Yandex     bool `json:"yandex"`
	YouTube    bool `json:"youtube"`
}

// Client is the part of an AdGuard persistent client that we manage.
type Client struct {
	Name                     string         `json:"name"`
	IDs                      []string       `json:"ids"`
	UseGlobalSettings        bool           `json:"use_global_settings"`
	FilteringEnabled         bool           `json:"filtering_enabled"`
	ParentalEnabled          bool           `json:"parental_enabled"`
	SafebrowsingEnabled      bool           `json:"safebrowsing_enabled"`
	SafeSearch               SafeSearch     `json:"safe_search"`
	UseGlobalBlockedServices bool           `json:"use_global_blocked_services"`
	BlockedServicesSchedule  map[string]any `json:"blocked_services_schedule"`
	BlockedServices          []string       `json:"blocked_services"`
	Upstreams                []string       `json:"upstreams"`
	Tags                     []string       `json:"tags"`
	IgnoreQuerylog           bool           `json:"ignore_querylog"`
	IgnoreStatistics         bool           `json:"ignore_statistics"`
}

func (a *AdGuard) clients() ([]Client, error) {
	var in struct {
		Clients []Client `json:"clients"`
	}
	if err := a.get("/control/clients", nil, &in); err != nil {
		return nil, err
	}
	return in.Clients, nil
}

func (a *AdGuard) addClient(c Client) error { return a.post("/control/clients/add", c) }
func (a *AdGuard) updateClient(old string, c Client) error {
	return a.post("/control/clients/update", map[string]any{"name": old, "data": c})
}
func (a *AdGuard) deleteClient(name string) error {
	return a.post("/control/clients/delete", map[string]string{"name": name})
}

func (a *AdGuard) userRules() ([]string, error) {
	var in struct {
		UserRules []string `json:"user_rules"`
	}
	if err := a.get("/control/filtering/status", nil, &in); err != nil {
		return nil, err
	}
	return in.UserRules, nil
}

func (a *AdGuard) setUserRules(rules []string) error {
	return a.post("/control/filtering/set_rules", map[string]any{"rules": rules})
}

// ensureCacheTTLMax caps how long AdGuard caches answers, so a newly blocked name stops
// resolving within about a minute.
func (a *AdGuard) ensureCacheTTLMax(sec int) error {
	var info struct {
		CacheTTLMax int `json:"cache_ttl_max"`
	}
	if err := a.get("/control/dns_info", nil, &info); err != nil {
		return err
	}
	if info.CacheTTLMax == sec {
		return nil
	}
	return a.post("/control/dns_config", map[string]int{"cache_ttl_max": sec})
}
