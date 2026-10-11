package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var la, _ = time.LoadLocation("America/Los_Angeles")

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, la)
	if err != nil {
		panic(err)
	}
	return t
}

func engineState() State {
	st := defaultState()
	weekdays := func(w [][2]int) map[string][][2]int {
		return map[string][][2]int{"1": w, "2": w, "3": w, "4": w, "5": w}
	}
	every := func(w [][2]int) map[string][][2]int {
		m := weekdays(w)
		m["0"], m["6"] = w, w
		return m
	}
	video := weekdays([][2]int{{17 * 60, 20 * 60}})
	video["0"] = [][2]int{{9 * 60, 21 * 60}}
	video["6"] = [][2]int{{9 * 60, 21 * 60}}
	st.Kids = []Kid{{ID: "maya", Name: "Maya", SafeSearch: true, Rules: []Rule{
		{ID: "r1", Name: "Adult", Dates: []string{"any"}, Sites: []string{"adult"}, Days: every([][2]int{})},
		{ID: "r2", Name: "No-school days", Dates: []string{"kind:holiday", "kind:break"}, Sites: []string{"video"}, Days: every([][2]int{{9 * 60, 21 * 60}})},
		{ID: "r3", Name: "Video after school", Dates: []string{"any"}, Sites: []string{"video"}, Days: video},
		{ID: "r4", Name: "Bedtime", Dates: []string{"any"}, Sites: []string{"internet"}, Days: every([][2]int{{7 * 60, 21*60 + 30}})},
	}}}
	return st
}

func blocked(t *testing.T, st State, now time.Time, key string) bool {
	t.Helper()
	b, _ := newEngine(st, now).Blocked(st.Kids[0], key)
	return b
}

func TestEngineRules(t *testing.T) {
	st := engineState()
	cases := []struct {
		when, key string
		want      bool
	}{
		{"2026-10-13 15:30", "video", true},     // Tuesday before 5 PM
		{"2026-10-13 17:00", "video", false},    // window opens at 5:00
		{"2026-10-13 20:00", "video", true},     // window end is exclusive
		{"2026-10-13 15:30", "games", false},    // no rule for games: allowed
		{"2026-10-13 15:30", "adult", true},     // always blocked
		{"2026-10-13 21:30", "internet", true},  // bedtime
		{"2026-10-13 21:30", "games", true},     // internet off blocks every group
		{"2026-10-17 10:00", "video", false},    // Saturday
		{"2026-11-11 10:00", "video", false},    // Veterans Day (district holiday, Wednesday)
		{"2026-12-22 10:00", "video", false},    // inside Winter Break (range from the feed)
		{"2027-01-04 10:00", "video", true},     // school is back (break guessed to end Fri 1/1)
		{"2026-10-13 06:59", "internet", true},  // before 7 AM
		{"2026-10-13 07:00", "internet", false}, // 7 AM
	}
	for _, c := range cases {
		if got := blocked(t, st, at(c.when), c.key); got != c.want {
			t.Errorf("%s %s: blocked=%v, want %v", c.when, c.key, got, c.want)
		}
	}
}

func TestEngineExceptionsAndPause(t *testing.T) {
	st := engineState()
	now := at("2026-10-13 15:30")
	ms := now.UnixMilli()
	st.Exceptions = []Exception{{ID: "x", Kid: "maya", Sites: []string{"video"}, Open: true, From: ms - 60000, Until: ms + 3600000}}
	if blocked(t, st, now, "video") {
		t.Error("open exception should allow video")
	}
	if blocked(t, st, now.Add(2*time.Hour), "video") {
		// 17:30 is inside the regular window anyway
		t.Error("17:30 should be open by rule")
	}
	st.Exceptions[0].Until = ms - 1
	if !blocked(t, st, now, "video") {
		t.Error("expired exception must not apply")
	}
	// An open exception can't open a group while Internet is off
	night := at("2026-10-13 22:00")
	st.Exceptions = []Exception{{ID: "x", Kid: "maya", Sites: []string{"video"}, Open: true, From: night.UnixMilli() - 1, Until: night.UnixMilli() + 3600000}}
	if !blocked(t, st, night, "video") {
		t.Error("internet off must win over a group exception")
	}
	st.Kids[0].Paused = true
	if !blocked(t, st, at("2026-10-13 18:00"), "internet") {
		t.Error("paused must block internet")
	}
}

func TestEngineDistrictEdits(t *testing.T) {
	st := engineState()
	st.District.Changed = map[string]DistrictChange{"d2026-12-21": {To: "2027-01-05"}}
	if blocked(t, st, at("2027-01-05 10:00"), "video") {
		t.Error("break extended to 1/5 by the parent")
	}
	st.District.Ignored = map[string]bool{"d2026-11-11": true}
	if !blocked(t, st, at("2026-11-11 10:00"), "video") {
		t.Error("ignored holiday must not count")
	}
}

func TestPlanAndRules(t *testing.T) {
	st := engineState()
	p := newEngine(st, at("2026-10-13 15:30")).Plan()[0]
	if p.InternetBlocked || !slices.Contains(p.Services, "youtube") || !p.Adult || !slices.Contains(p.Sites, "pornhub.com") {
		t.Errorf("plan: %+v", p)
	}
	if slices.Contains(p.Services, "roblox") {
		t.Error("games are not blocked at 15:30")
	}
	got := mergeRules([]string{"||a.com^", ruleBegin, "old", ruleEnd, "||b.com^"}, []string{"new"})
	want := []string{"||a.com^", "||b.com^", ruleBegin, "new", ruleEnd}
	if !slices.Equal(got, want) {
		t.Errorf("mergeRules: %v", got)
	}
	if got := mergeRules(want, nil); !slices.Equal(got, []string{"||a.com^", "||b.com^"}) {
		t.Errorf("mergeRules empty: %v", got)
	}
	names := clientNames([]Kid{{ID: "a", Name: "Leo's 'iPad'"}, {ID: "b", Name: "Leos iPad"}, {ID: "c", Name: "!!"}, {ID: "d", Name: "Anna & Ben"}})
	if names["a"] != "HH Leos iPad" || names["b"] != "HH Leos iPad-b" || names["c"] != "HH c" || names["d"] != "HH Anna Ben" {
		t.Errorf("client names: %v", names)
	}
	nft := renderNft([]string{"aa:bb:cc:dd:ee:ff"}, []string{"192.168.0.0/24"})
	if !strings.Contains(nft, "elements = { aa:bb:cc:dd:ee:ff }") || !strings.Contains(nft, "192.168.0.0/24") {
		t.Errorf("nft:\n%s", nft)
	}
}
