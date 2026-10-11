package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
)

// State is everything parents configure. Field shapes match the web page's data model.
type State struct {
	// Version goes up on every save; a client sends the version it edited (optimistic locking).
	Version    int64       `json:"version"`
	Kids       []Kid       `json:"kids"`
	Groups     []Group     `json:"groups"`
	Devices    []Device    `json:"devices"`
	Calendar   []CalEntry  `json:"calendar"`
	District   District    `json:"district"`
	Exceptions []Exception `json:"exceptions"`
	Favorites  []Favorite  `json:"favorites"`
}

type Kid struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	SafeSearch bool   `json:"safeSearch"`
	Paused     bool   `json:"paused"`
	Rules      []Rule `json:"rules"`
}

// Rule: dates (any of) + sites (any of) + open windows per weekday ("0"=Sun … "6"=Sat).
// A weekday missing from Days means the rule does not apply that day. Windows are minutes [from,to).
type Rule struct {
	ID    string              `json:"id"`
	Name  string              `json:"name"`
	Dates []string            `json:"dates"`
	Sites []string            `json:"sites"`
	Days  map[string][][2]int `json:"days"`
}

type Group struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []Member `json:"members"`
}

// Member kinds: "service" (AdGuard blocked-service id), "site" (domain), "filter" (category).
type Member struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type Device struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	Kid      string `json:"kid"`
	Reviewed bool   `json:"reviewed,omitempty"` // parent saw it and said it is not a child's device
}

type CalEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// District: Feed, the sync times, the last error and the notices are owned by the server
// (DistrictSync); the parent owns Changed (end-date edits) and Ignored.
type District struct {
	LastSync    string                    `json:"lastSync"`
	LastAttempt string                    `json:"lastAttempt"`
	LastError   string                    `json:"lastError"`
	Feed        []FeedItem                `json:"feed"`
	Notices     []DistrictNotice          `json:"notices"`
	Changed     map[string]DistrictChange `json:"changed"`
	Ignored     map[string]bool           `json:"ignored"`
}

type DistrictChange struct {
	To string `json:"to,omitempty"`
}

type Exception struct {
	ID    string   `json:"id"`
	Kid   string   `json:"kid"`
	Sites []string `json:"sites"`
	Open  bool     `json:"open"`
	From  int64    `json:"from"`
	Until int64    `json:"until"`
	Note  string   `json:"note"`
}

type Favorite struct {
	ID      string   `json:"id"`
	Kid     string   `json:"kid"`
	Sites   []string `json:"sites"`
	Open    bool     `json:"open"`
	Minutes int      `json:"minutes"`
	Note    string   `json:"note"`
}

func defaultState() State {
	svc := func(id string) Member { return Member{Kind: "service", ID: id} }
	site := func(d string) Member { return Member{Kind: "site", ID: d} }
	return State{
		Kids: []Kid{},
		Groups: []Group{
			{ID: "video", Name: "Video & social", Members: []Member{svc("youtube"), svc("tiktok"), svc("instagram"), svc("snapchat"), svc("twitch")}},
			{ID: "games", Name: "Games", Members: []Member{svc("roblox"), svc("epic_games"), svc("steam")}},
			{ID: "adult", Name: "Adult", Members: []Member{{Kind: "filter", ID: "adult", Label: "Adult content filter"}, site("pornhub.com"), site("xvideos.com")}},
			{ID: "school", Name: "School", Members: []Member{site("khanacademy.org"), site("classroom.google.com"), site("wikipedia.org")}},
		},
		Devices:    []Device{},
		Calendar:   []CalEntry{},
		District:   District{Feed: defaultFeed(), Notices: []DistrictNotice{}, Changed: map[string]DistrictChange{}, Ignored: map[string]bool{}},
		Exceptions: []Exception{},
		Favorites:  []Favorite{},
	}
}

type Store struct {
	mu    sync.Mutex
	path  string
	state State
}

func openStore(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.state = defaultState()
		return s, s.saveLocked()
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.state); err != nil {
		return nil, err
	}
	// Feeds saved before step 5 had first days only: start again from the seed
	if len(s.state.District.Feed) == 0 || s.state.District.Feed[0].To == "" {
		s.state.District.Feed = defaultFeed()
	}
	if s.state.District.Notices == nil {
		s.state.District.Notices = []DistrictNotice{}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, raw, 0o600)
}

// Snapshot returns a deep copy, safe to use without the lock.
func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyLocked()
}

// ErrConflict means the client edited an older version than the one stored.
var ErrConflict = errors.New("state changed since it was loaded")

// Replace stores next if the client edited the current version. Fields the client doesn't own
// (the district feed, sync status and notices) are kept from the stored state; the device list
// is merged.
func (s *Store) Replace(baseVersion int64, next State, seen func(mac string) bool) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if baseVersion != s.state.Version {
		return State{}, ErrConflict
	}
	next.Devices = mergeDevices(s.state.Devices, next.Devices, next.Kids, seen)
	cur := s.state.District
	next.District.LastSync, next.District.LastAttempt, next.District.LastError = cur.LastSync, cur.LastAttempt, cur.LastError
	next.District.Feed, next.District.Notices = cur.Feed, cur.Notices
	next.Version = s.state.Version + 1
	prev := s.state
	s.state = next
	if err := s.saveLocked(); err != nil {
		s.state = prev
		return State{}, err
	}
	return s.copyLocked(), nil
}

// UpdateDistrict changes the server-owned district fields and bumps the version, so open pages
// reload them.
func (s *Store) UpdateDistrict(f func(d *District)) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.copyLocked()
	f(&s.state.District)
	s.state.Version++
	if err := s.saveLocked(); err != nil {
		s.state = prev
		return State{}, err
	}
	return s.copyLocked(), nil
}

func (s *Store) copyLocked() State {
	raw, _ := json.Marshal(s.state)
	var out State
	_ = json.Unmarshal(raw, &out)
	return out
}

// mergeDevices takes the page's device list (name and child per MAC) as the new stored list.
// Only MACs seen on the network or already stored are accepted: devices are discovered, never
// invented by the page. Entries without a name, a child or a review mark are not worth storing.
func mergeDevices(stored, edited []Device, kids []Kid, seen func(mac string) bool) []Device {
	kidOK := map[string]bool{"": true}
	for _, k := range kids {
		kidOK[k.ID] = true
	}
	wasStored := map[string]bool{}
	for _, d := range stored {
		wasStored[d.MAC] = true
	}
	out := []Device{}
	dup := map[string]bool{}
	for _, d := range edited {
		d.MAC = strings.ToLower(d.MAC)
		if !reMAC.MatchString(d.MAC) || dup[d.MAC] || !(wasStored[d.MAC] || seen(d.MAC)) {
			continue
		}
		dup[d.MAC] = true
		d.Name = cleanText(d.Name, 60)
		if !kidOK[d.Kid] {
			d.Kid = ""
		}
		if d.Name != "" || d.Kid != "" || d.Reviewed {
			out = append(out, d)
		}
	}
	return out
}
