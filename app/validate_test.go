package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func sampleState(now time.Time) State {
	st := defaultState()
	st.Kids = []Kid{{ID: "k1", Name: " Maya ", Color: "#3f7d6e", SafeSearch: true, Rules: []Rule{
		{ID: "r1", Name: "Video", Dates: []string{"any", "kind:break"}, Sites: []string{"video"},
			Days: map[string][][2]int{"1": {{1020, 1200}, {420, 480}}, "0": {}}},
	}}}
	ms := now.UnixMilli()
	st.Exceptions = []Exception{
		{ID: "x1", Kid: "k1", Sites: []string{"video"}, Open: true, From: ms - 60000, Until: ms + 3600000},
		{ID: "x2", Kid: "k1", Sites: []string{"games"}, Open: true, From: ms, Until: ms + 3600000},
		{ID: "x0", Kid: "k1", Sites: []string{"games"}, Open: true, From: ms - 7200000, Until: ms - 3600000},
	}
	st.Favorites = []Favorite{{ID: "f1", Kid: "k1", Sites: []string{"internet"}, Minutes: 60}}
	return st
}

func TestValidateNormalizes(t *testing.T) {
	now := time.Now()
	st := sampleState(now)
	if err := validateState(&st, now); err != nil {
		t.Fatal(err)
	}
	if st.Kids[0].Name != "Maya" {
		t.Errorf("name not trimmed: %q", st.Kids[0].Name)
	}
	if w := st.Kids[0].Rules[0].Days["1"]; w[0][0] != 420 {
		t.Errorf("windows not sorted: %v", w)
	}
	if len(st.Exceptions) != 1 || st.Exceptions[0].ID != "x2" {
		t.Errorf("want only the newest live exception, got %+v", st.Exceptions)
	}
}

func TestValidateRejects(t *testing.T) {
	now := time.Now()
	cases := map[string]func(*State){
		"unknown group": func(s *State) { s.Kids[0].Rules[0].Sites = []string{"nope"} },
		"reversed time": func(s *State) { s.Kids[0].Rules[0].Days["1"] = [][2]int{{600, 500}} },
		"overlap":       func(s *State) { s.Kids[0].Rules[0].Days["1"] = [][2]int{{0, 600}, {500, 700}} },
		"bad weekday":   func(s *State) { s.Kids[0].Rules[0].Days["7"] = [][2]int{} },
		"no days":       func(s *State) { s.Kids[0].Rules[0].Days = map[string][][2]int{} },
		"bad domain": func(s *State) {
			s.Groups[0].Members = append(s.Groups[0].Members, Member{Kind: "site", ID: "not a domain"})
		},
		"orphan fav":     func(s *State) { s.Favorites[0].Kid = "ghost" },
		"bad length":     func(s *State) { s.Favorites[0].Minutes = 45 },
		"long exception": func(s *State) { s.Exceptions[0].Until = s.Exceptions[0].From + 24*3600000 },
		"dup kid":        func(s *State) { s.Kids = append(s.Kids, s.Kids[0]) },
		"bad calendar": func(s *State) {
			s.Calendar = []CalEntry{{ID: "c1", Name: "x", From: "2027-01-05", To: "2027-01-01", Kind: "break"}}
		},
	}
	for name, mutate := range cases {
		st := sampleState(now)
		mutate(&st)
		if err := validateState(&st, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReplaceVersions(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.state.Devices = []Device{{MAC: "aa:bb:cc:dd:ee:ff", Name: "iPad"}}
	seen := func(mac string) bool { return mac == "aa:bb:cc:dd:ee:01" }
	now := time.Now()
	st := sampleState(now)
	_ = validateState(&st, now)
	st.Devices = []Device{{MAC: "AA:BB:CC:DD:EE:FF", Name: "Maya's iPad", Kid: "k1"}, {MAC: "11:22:33:44:55:66", Name: "made up"},
		{MAC: "aa:bb:cc:dd:ee:01", Kid: "k1"}, {MAC: "aa:bb:cc:dd:ee:02", Kid: "ghost"}}
	saved, err := s.Replace(0, st, seen)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || len(saved.Devices) != 2 || saved.Devices[0].MAC != "aa:bb:cc:dd:ee:ff" || saved.Devices[0].Kid != "k1" || saved.Devices[1].MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("unexpected save: %+v", saved.Devices)
	}
	if _, err := s.Replace(0, st, seen); !errors.Is(err, ErrConflict) {
		t.Errorf("stale version accepted: %v", err)
	}
	// Reopening reads the saved file back
	s2, err := openStore(s.path)
	if err != nil || s2.Snapshot().Version != 1 {
		t.Errorf("reopen: %v %+v", err, s2.Snapshot().Version)
	}
}
