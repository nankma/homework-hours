package main

import (
	"slices"
	"strconv"
	"time"
	_ "time/tzdata" // OpenWrt has no zoneinfo; rule times are local (America/Los_Angeles)
)

// Engine evaluates the rules exactly like the page (status() in web/index.html):
// paused → exception → first matching rule → allowed. If Internet is blocked, every group is.
// Profiles form a tree: each step looks at the profile first, then the profiles above it.
type Engine struct {
	st      State
	entries []CalEntry
	now     time.Time // in the household's time zone
	byID    map[string]Kid
}

func newEngine(st State, now time.Time) *Engine {
	e := &Engine{st: st, entries: append(districtEntries(st.District), st.Calendar...), now: now, byID: map[string]Kid{}}
	for _, k := range st.Kids {
		e.byID[k.ID] = k
	}
	return e
}

// chain is k followed by the profiles above it, nearest first. A missing parent or a loop
// (validation forbids both) ends the chain.
func (e *Engine) chain(k Kid) []Kid {
	out := []Kid{k}
	for len(out) <= maxDepth {
		p, ok := e.byID[out[len(out)-1].Parent]
		if !ok || slices.ContainsFunc(out, func(x Kid) bool { return x.ID == p.ID }) {
			break
		}
		out = append(out, p)
	}
	return out
}

func (e *Engine) entriesOn(day string) []CalEntry {
	var out []CalEntry
	for _, c := range e.entries {
		if day >= c.From && day <= c.To {
			out = append(out, c)
		}
	}
	return out
}

func (e *Engine) dateMatches(r Rule) bool {
	ents := e.entriesOn(e.now.Format("2006-01-02"))
	for _, c := range r.Dates {
		switch {
		case c == "any":
			return true
		case len(c) > 5 && c[:5] == "kind:":
			for _, en := range ents {
				if en.Kind == c[5:] {
					return true
				}
			}
		case len(c) > 6 && c[:6] == "entry:":
			for _, en := range ents {
				if en.ID == c[6:] {
					return true
				}
			}
		}
	}
	return false
}

// rules is the inherited rule list: the profile's own rules, then its parent's, and so on.
func (e *Engine) rules(k Kid) []Rule {
	var out []Rule
	for _, p := range e.chain(k) {
		out = append(out, p.Rules...)
	}
	return out
}

// windowOpen: the first rule (top to bottom) that covers key, today's weekday and today's date
// decides; no such rule means open.
func (e *Engine) windowOpen(k Kid, key string) (open bool, rule string) {
	dow := strconv.Itoa(int(e.now.Weekday()))
	t := e.now.Hour()*60 + e.now.Minute()
	for _, r := range e.rules(k) {
		wins, ok := r.Days[dow]
		if !ok || !slices.Contains(r.Sites, key) || !e.dateMatches(r) {
			continue
		}
		for _, w := range wins {
			if t >= w[0] && t < w[1] {
				return true, r.Name
			}
		}
		return false, r.Name
	}
	return true, ""
}

// exception: the nearest profile (own first, then upward) with a live exception covering key
// decides; within one profile the last one listed wins.
func (e *Engine) exception(k Kid, key string) *Exception {
	ms := e.now.UnixMilli()
	for _, p := range e.chain(k) {
		var found *Exception
		for i, x := range e.st.Exceptions {
			if x.Kid == p.ID && x.Until > ms && x.From <= ms && slices.Contains(x.Sites, key) {
				found = &e.st.Exceptions[i]
			}
		}
		if found != nil {
			return found
		}
	}
	return nil
}

// Paused: the profile or one above it is paused.
func (e *Engine) Paused(k Kid) bool {
	return slices.ContainsFunc(e.chain(k), func(p Kid) bool { return p.Paused })
}

// SafeSearch: on for the profile or one above it.
func (e *Engine) SafeSearch(k Kid) bool {
	return slices.ContainsFunc(e.chain(k), func(p Kid) bool { return p.SafeSearch })
}

// Blocked reports whether key ("internet" or a group id) is blocked for k now, and why.
func (e *Engine) Blocked(k Kid, key string) (bool, string) {
	if e.Paused(k) {
		return true, "paused"
	}
	if key != "internet" {
		if net, _ := e.Blocked(k, "internet"); net {
			return true, "internet is off"
		}
	}
	if x := e.exception(k, key); x != nil {
		if x.Open {
			return false, "exception"
		}
		return true, "exception"
	}
	open, rule := e.windowOpen(k, key)
	if rule == "" {
		return false, "no rule"
	}
	return !open, "rule: " + rule
}

// KidPlan is what has to be enforced for one child right now.
type KidPlan struct {
	Kid             Kid
	InternetBlocked bool
	BlockedGroups   []string // group ids
	Services        []string // AdGuard blocked-service ids
	Sites           []string // domains
	Adult           bool     // AdGuard parental (adult content) filter
	SafeSearch      bool     // own or inherited
}

func (e *Engine) Plan() []KidPlan {
	var plans []KidPlan
	for _, k := range e.st.Kids {
		p := KidPlan{Kid: k, SafeSearch: e.SafeSearch(k)}
		p.InternetBlocked, _ = e.Blocked(k, "internet")
		for _, g := range e.st.Groups {
			if b, _ := e.Blocked(k, g.ID); !b {
				continue
			}
			p.BlockedGroups = append(p.BlockedGroups, g.ID)
			for _, m := range g.Members {
				switch m.Kind {
				case "service":
					p.Services = append(p.Services, m.ID)
				case "site":
					p.Sites = append(p.Sites, m.ID)
				case "filter":
					p.Adult = p.Adult || m.ID == "adult"
				}
			}
		}
		slices.Sort(p.Services)
		p.Services = slices.Compact(p.Services)
		slices.Sort(p.Sites)
		p.Sites = slices.Compact(p.Sites)
		plans = append(plans, p)
	}
	return plans
}
