package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

var (
	reID      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	reColor   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	reService = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	reMAC     = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)
	reDomain  = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)
)

var (
	entryKinds     = map[string]bool{"holiday": true, "break": true, "custom": true}
	filterIDs      = map[string]bool{"adult": true}
	favoriteLength = map[int]bool{30: true, 60: true, 120: true, 180: true, 240: true, 360: true, 720: true}
)

// Longest exception the page offers, plus slack for clock differences between phone and CM4.
const maxExceptionMs = (12*60 + 5) * 60 * 1000

// cleanText trims, drops control characters and caps the length (in runes).
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// validateState checks a state sent by the page and normalizes it in place: text is cleaned,
// time windows sorted, expired exceptions dropped, and only the newest exception per child kept.
// It returns a message a parent can read.
func validateState(st *State, now time.Time) error {
	if len(st.Kids) > 20 || len(st.Groups) > 50 || len(st.Calendar) > 500 || len(st.Favorites) > 100 {
		return fmt.Errorf("too many entries")
	}

	groups := map[string]bool{}
	for i := range st.Groups {
		g := &st.Groups[i]
		g.Name = cleanText(g.Name, 60)
		if !reID.MatchString(g.ID) || g.ID == "internet" || groups[g.ID] {
			return fmt.Errorf("bad site group id %q", g.ID)
		}
		if g.Name == "" {
			return fmt.Errorf("a site group needs a name")
		}
		groups[g.ID] = true
		if len(g.Members) > 500 {
			return fmt.Errorf("group %s has too many members", g.Name)
		}
		for j := range g.Members {
			m := &g.Members[j]
			m.Label = cleanText(m.Label, 60)
			switch m.Kind {
			case "service":
				if !reService.MatchString(m.ID) {
					return fmt.Errorf("bad service %q", m.ID)
				}
			case "site":
				m.ID = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(m.ID)), ".")
				if len(m.ID) > 253 || !reDomain.MatchString(m.ID) {
					return fmt.Errorf("%q is not a website address like example.com", m.ID)
				}
			case "filter":
				if !filterIDs[m.ID] {
					return fmt.Errorf("unknown category %q", m.ID)
				}
			default:
				return fmt.Errorf("unknown member kind %q", m.Kind)
			}
		}
	}
	validSites := func(sites []string) error {
		if len(sites) == 0 {
			return fmt.Errorf("pick at least one site group or Internet")
		}
		for _, s := range sites {
			if s != "internet" && !groups[s] {
				return fmt.Errorf("unknown site group %q", s)
			}
		}
		return nil
	}

	entries := map[string]bool{}
	for i := range st.Calendar {
		c := &st.Calendar[i]
		c.Name = cleanText(c.Name, 80)
		c.Source = "manual"
		if !reID.MatchString(c.ID) || entries[c.ID] {
			return fmt.Errorf("bad calendar id %q", c.ID)
		}
		entries[c.ID] = true
		if c.Name == "" || !validDate(c.From) || !validDate(c.To) || c.To < c.From || !entryKinds[c.Kind] {
			return fmt.Errorf("calendar entry %q needs a name, a kind and a valid date range", c.Name)
		}
	}
	if st.District.Changed == nil {
		st.District.Changed = map[string]DistrictChange{}
	}
	if st.District.Ignored == nil {
		st.District.Ignored = map[string]bool{}
	}
	for id, ch := range st.District.Changed {
		if !reID.MatchString(id) || (ch.To != "" && !validDate(ch.To)) {
			return fmt.Errorf("bad district change %q", id)
		}
	}
	for id := range st.District.Ignored {
		if !reID.MatchString(id) {
			return fmt.Errorf("bad district entry %q", id)
		}
	}

	kids := map[string]bool{}
	for i := range st.Kids {
		k := &st.Kids[i]
		k.Name = cleanText(k.Name, 40)
		if !reID.MatchString(k.ID) || kids[k.ID] {
			return fmt.Errorf("bad child id %q", k.ID)
		}
		if k.Name == "" {
			return fmt.Errorf("a child needs a name")
		}
		if !reColor.MatchString(k.Color) {
			k.Color = "#3f7d6e"
		}
		kids[k.ID] = true
		if len(k.Rules) > 50 {
			return fmt.Errorf("%s has too many rules", k.Name)
		}
		rules := map[string]bool{}
		for j := range k.Rules {
			r := &k.Rules[j]
			r.Name = cleanText(r.Name, 60)
			if !reID.MatchString(r.ID) || rules[r.ID] {
				return fmt.Errorf("bad rule id %q", r.ID)
			}
			rules[r.ID] = true
			if r.Name == "" {
				return fmt.Errorf("a rule of %s needs a name", k.Name)
			}
			if len(r.Dates) == 0 {
				return fmt.Errorf("rule %q: pick at least one date option", r.Name)
			}
			for _, d := range r.Dates {
				switch {
				case d == "any":
				case strings.HasPrefix(d, "kind:") && entryKinds[d[5:]]:
				case strings.HasPrefix(d, "entry:") && reID.MatchString(d[6:]):
					// May point at a district entry, which lives outside this state
				default:
					return fmt.Errorf("rule %q: bad date option %q", r.Name, d)
				}
			}
			if err := validSites(r.Sites); err != nil {
				return fmt.Errorf("rule %q: %v", r.Name, err)
			}
			if len(r.Days) == 0 {
				return fmt.Errorf("rule %q: check at least one day of the week", r.Name)
			}
			for day, wins := range r.Days {
				if len(day) != 1 || day[0] < '0' || day[0] > '6' {
					return fmt.Errorf("rule %q: bad weekday %q", r.Name, day)
				}
				if len(wins) > 12 {
					return fmt.Errorf("rule %q: too many time windows", r.Name)
				}
				sort.Slice(wins, func(a, b int) bool { return wins[a][0] < wins[b][0] })
				for w, win := range wins {
					if win[0] < 0 || win[1] > 1440 || win[0] >= win[1] {
						return fmt.Errorf("rule %q: a time window must end after it starts", r.Name)
					}
					if w > 0 && win[0] < wins[w-1][1] {
						return fmt.Errorf("rule %q: time windows overlap", r.Name)
					}
				}
				if wins == nil {
					r.Days[day] = [][2]int{}
				}
			}
		}
	}

	nowMs := now.UnixMilli()
	newest := map[string]Exception{}
	for _, x := range st.Exceptions {
		x.Note = cleanText(x.Note, 80)
		if !reID.MatchString(x.ID) || !kids[x.Kid] {
			return fmt.Errorf("bad exception %q", x.ID)
		}
		if err := validSites(x.Sites); err != nil {
			return fmt.Errorf("exception: %v", err)
		}
		if x.Until <= x.From || x.Until-x.From > maxExceptionMs {
			return fmt.Errorf("exception: bad length")
		}
		if x.Until <= nowMs {
			continue
		}
		if cur, ok := newest[x.Kid]; !ok || x.From >= cur.From {
			newest[x.Kid] = x
		}
	}
	st.Exceptions = make([]Exception, 0, len(newest))
	for _, x := range newest {
		st.Exceptions = append(st.Exceptions, x)
	}
	sort.Slice(st.Exceptions, func(a, b int) bool { return st.Exceptions[a].From < st.Exceptions[b].From })

	favs := map[string]bool{}
	for i := range st.Favorites {
		f := &st.Favorites[i]
		f.Note = cleanText(f.Note, 80)
		if !reID.MatchString(f.ID) || favs[f.ID] || !kids[f.Kid] {
			return fmt.Errorf("bad favorite %q", f.ID)
		}
		favs[f.ID] = true
		if err := validSites(f.Sites); err != nil {
			return fmt.Errorf("favorite: %v", err)
		}
		if !favoriteLength[f.Minutes] {
			return fmt.Errorf("favorite: bad length")
		}
	}

	if st.Kids == nil {
		st.Kids = []Kid{}
	}
	if st.Groups == nil {
		st.Groups = []Group{}
	}
	if st.Calendar == nil {
		st.Calendar = []CalEntry{}
	}
	if st.Favorites == nil {
		st.Favorites = []Favorite{}
	}
	for i := range st.Kids {
		if st.Kids[i].Rules == nil {
			st.Kids[i].Rules = []Rule{}
		}
	}
	for i := range st.Groups {
		if st.Groups[i].Members == nil {
			st.Groups[i].Members = []Member{}
		}
	}
	return nil
}
