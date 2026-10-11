package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// FeedItem is one run of no-school days from the Bellevue School District "Holidays and
// Closures" calendar: first day, last day and the district's title.
type FeedItem struct {
	Date string `json:"date"`
	To   string `json:"to"`
	Note string `json:"note"`
}

// DistrictNotice tells the parent what a sync changed (shown until dismissed).
type DistrictNotice struct {
	At   string `json:"at"`
	Text string `json:"text"`
}

const districtURL = "https://www.bsd405.org/fs/calendar-manager/events.ics?calendar_ids[]=2"

// The feed as fetched on 2026-10-09, used until the first successful sync.
//
//go:embed seed/bsd-holidays-2026-10-09.ics
var seedICS string

func defaultFeed() []FeedItem {
	feed, err := parseHolidayICS(seedICS, time.UTC)
	if err != nil {
		panic("seed calendar: " + err.Error())
	}
	return feed
}

// districtName shortens the district's title: "No school, Veterans Day observance, all district
// offices closed" → "Veterans Day observance".
func districtName(note string) string {
	n := strings.TrimPrefix(note, "No school, ")
	n = strings.TrimSuffix(n, ", all district offices closed")
	if n != "" {
		n = strings.ToUpper(n[:1]) + n[1:]
	}
	return n
}

// districtBase turns the feed into calendar entries before the parent's edits: titles with
// "Break" are breaks, everything else a holiday; holidays inside a break are dropped. The page
// does the same (districtEntries in web/index.html); keep the two in step.
func districtBase(feed []FeedItem) []CalEntry {
	var breaks, days []CalEntry
	for _, f := range feed {
		to := f.To
		if to == "" {
			to = f.Date
		}
		e := CalEntry{ID: "d" + f.Date, Name: districtName(f.Note), From: f.Date, To: to, Kind: "holiday", Source: "district"}
		if strings.Contains(strings.ToLower(f.Note), "break") {
			e.Kind = "break"
			breaks = append(breaks, e)
		} else {
			days = append(days, e)
		}
	}
	out := breaks
	for _, e := range days {
		inside := false
		for _, b := range breaks {
			if e.From >= b.From && e.From <= b.To {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, e)
		}
	}
	// One entry per first day (ids are "d<first day>"): the longest wins
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To > out[j].To
	})
	kept := out[:0]
	for _, e := range out {
		if len(kept) > 0 && kept[len(kept)-1].ID == e.ID {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// districtEntries applies the parent's ignores and end-date edits.
func districtEntries(d District) []CalEntry {
	var out []CalEntry
	for _, e := range districtBase(d.Feed) {
		if d.Ignored[e.ID] {
			continue
		}
		if ch, ok := d.Changed[e.ID]; ok && ch.To != "" && ch.To >= e.From {
			e.To = ch.To
		}
		out = append(out, e)
	}
	return out
}

func shortDate(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("Mon 1/2/06")
}

func entrySpan(e CalEntry) string {
	if e.From == e.To {
		return shortDate(e.From)
	}
	return shortDate(e.From) + " – " + shortDate(e.To)
}

// districtChanges describes how the new feed differs from the old one, including parent edits
// that no longer match an entry (the district moved the first day).
func districtChanges(old District, feed []FeedItem) []string {
	before, after := map[string]CalEntry{}, map[string]CalEntry{}
	for _, e := range districtBase(old.Feed) {
		before[e.ID] = e
	}
	var msgs []string
	for _, e := range districtBase(feed) {
		after[e.ID] = e
		if b, ok := before[e.ID]; !ok {
			msgs = append(msgs, fmt.Sprintf("Added: %s, %s.", e.Name, entrySpan(e)))
		} else if b.To != e.To || b.Kind != e.Kind {
			msgs = append(msgs, fmt.Sprintf("Changed: %s is now %s (was %s).", e.Name, entrySpan(e), entrySpan(b)))
		}
	}
	for _, b := range districtBase(old.Feed) {
		if _, ok := after[b.ID]; !ok {
			msgs = append(msgs, fmt.Sprintf("Removed: %s, %s.", b.Name, entrySpan(b)))
			if _, edited := old.Changed[b.ID]; edited || old.Ignored[b.ID] {
				msgs = append(msgs, fmt.Sprintf("Your edit to %s no longer applies; check the Calendar tab.", b.Name))
			}
		}
	}
	return msgs
}

// DistrictSync refreshes state.district.feed from the district calendar: at startup, daily at
// 03:00, hourly after a failure, and on demand ("Sync now").
type DistrictSync struct {
	url     string
	store   *Store
	loc     *time.Location
	changed func()
	client  *http.Client
	mu      sync.Mutex
}

func newDistrictSync(store *Store, loc *time.Location, changed func()) *DistrictSync {
	return &DistrictSync{url: districtURL, store: store, loc: loc, changed: changed, client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *DistrictSync) run(ctx context.Context) {
	wait := 20 * time.Second // let NTP and DNS settle after boot
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if _, err := s.Sync(ctx); err != nil {
			log.Printf("district sync: %v", err)
			wait = time.Hour
			continue
		}
		now := time.Now().In(s.loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, s.loc)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		wait = next.Sub(now)
	}
}

// Sync fetches and stores the feed. On failure the old feed stays and the error is recorded.
func (s *DistrictSync) Sync(ctx context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	feed, err := s.fetch(ctx)
	now := time.Now()
	st, saveErr := s.store.UpdateDistrict(func(d *District) {
		d.LastAttempt = now.Format(time.RFC3339)
		if err != nil {
			d.LastError = err.Error()
			return
		}
		d.LastError = ""
		d.LastSync = now.Format(time.RFC3339)
		for _, m := range districtChanges(*d, feed) {
			d.Notices = append(d.Notices, DistrictNotice{At: d.LastSync, Text: m})
		}
		if n := len(d.Notices); n > 30 {
			d.Notices = d.Notices[n-30:]
		}
		d.Feed = feed
	})
	if saveErr != nil {
		return st, saveErr
	}
	if err == nil {
		s.changed()
	}
	return st, err
}

func (s *DistrictSync) fetch(ctx context.Context) ([]FeedItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the district website: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the district website answered %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	feed, err := parseHolidayICS(string(raw), s.loc)
	if err != nil {
		return nil, fmt.Errorf("the district calendar could not be read: %w", err)
	}
	return feed, nil
}
