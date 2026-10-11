package main

import (
	"slices"
	"strconv"
	"time"
	_ "time/tzdata" // OpenWrt has no zoneinfo; rule times are local (America/Los_Angeles)
)

// Engine evaluates the rules exactly like the page (status() in web/index.html):
// paused → exception → first matching rule → allowed. If Internet is blocked, every group is.
type Engine struct {
	st      State
	entries []CalEntry
	now     time.Time // in the household's time zone
}

func newEngine(st State, now time.Time) *Engine {
	return &Engine{st: st, entries: append(districtEntries(st.District), st.Calendar...), now: now}
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

// windowOpen: the first rule (top to bottom) that covers key, today's weekday and today's date
// decides; no such rule means open.
func (e *Engine) windowOpen(k Kid, key string) (open bool, rule string) {
	dow := strconv.Itoa(int(e.now.Weekday()))
	t := e.now.Hour()*60 + e.now.Minute()
	for _, r := range k.Rules {
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

func (e *Engine) exception(k Kid, key string) *Exception {
	ms := e.now.UnixMilli()
	var found *Exception
	for i, x := range e.st.Exceptions {
		if x.Kid == k.ID && x.Until > ms && x.From <= ms && slices.Contains(x.Sites, key) {
			found = &e.st.Exceptions[i]
		}
	}
	return found
}

// Blocked reports whether key ("internet" or a group id) is blocked for k now, and why.
func (e *Engine) Blocked(k Kid, key string) (bool, string) {
	if k.Paused {
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
}

func (e *Engine) Plan() []KidPlan {
	var plans []KidPlan
	for _, k := range e.st.Kids {
		p := KidPlan{Kid: k}
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
