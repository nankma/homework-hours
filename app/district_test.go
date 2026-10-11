package main

import (
	"strings"
	"testing"
)

func TestParseDistrictFeed(t *testing.T) {
	feed, err := parseHolidayICS(seedICS, la)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range feed {
		got[f.Date+" "+districtName(f.Note)] = f.To
	}
	want := map[string]string{
		"2026-10-09 No school day":             "2026-10-09",
		"2026-11-26 Thanksgiving":              "2026-11-27", // COUNT=2
		"2026-12-21 Winter Break":              "2027-01-01", // weekdays until 1/1, across a weekend
		"2026-12-24 Observed holiday":          "2026-12-25", // UNTIL date is inclusive
		"2026-12-31 New Year holiday observed": "2027-01-01",
		"2027-02-15 Mid-Winter Break":          "2027-02-19",
		"2027-04-12 Spring Break":              "2027-04-16",
		"2027-06-24 Non-school days":           "2027-06-25",
	}
	for k, to := range want {
		if got[k] != to {
			t.Errorf("%s: to=%q, want %q", k, got[k], to)
		}
	}
	// The cancelled duplicate Mid-Winter Break (both days in EXDATE) adds nothing
	n := 0
	for _, f := range feed {
		if strings.Contains(f.Note, "Mid-Winter") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("Mid-Winter Break items: %d, want 1", n)
	}

	entries := districtBase(feed)
	for _, e := range entries {
		if e.From == "2026-12-24" || e.From == "2026-12-31" || (e.From == "2027-02-15" && e.Kind == "holiday") {
			t.Errorf("holiday inside a break kept: %+v", e)
		}
	}
	if len(entries) != 14 {
		t.Errorf("%d entries, want 14: %+v", len(entries), entries)
	}
}

func TestDistrictChanges(t *testing.T) {
	old := District{Feed: []FeedItem{
		{"2026-12-21", "2027-01-01", "No school, Winter Break"},
		{"2027-01-18", "2027-01-18", "No school, MLK Day"},
		{"2027-03-19", "2027-03-19", "No school day"},
	}, Changed: map[string]DistrictChange{"d2027-03-19": {To: "2027-03-20"}}}
	feed := []FeedItem{
		{"2026-12-21", "2027-01-04", "No school, Winter Break"},
		{"2027-01-18", "2027-01-18", "No school, MLK Day"},
		{"2027-03-22", "2027-03-22", "No school day"},
	}
	msgs := strings.Join(districtChanges(old, feed), "\n")
	for _, w := range []string{"Changed: Winter Break is now Mon 12/21/26 – Mon 1/4/27", "Added: No school day, Mon 3/22/27", "Removed: No school day, Fri 3/19/27", "Your edit to No school day no longer applies"} {
		if !strings.Contains(msgs, w) {
			t.Errorf("missing %q in:\n%s", w, msgs)
		}
	}
	if strings.Contains(msgs, "MLK") {
		t.Errorf("unchanged entry reported:\n%s", msgs)
	}
	if districtChanges(old, old.Feed) != nil {
		t.Error("no change must give no notice")
	}
}

func TestICSRules(t *testing.T) {
	ics := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20270104\r\nDTEND;VALUE=DATE:20270105\r\nSUMMARY:Every other\r\n  Monday\r\nRRULE:FREQ=WEEKLY;INTERVAL=2;COUNT=3\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20270301\r\nDTEND;VALUE=DATE:20270303\r\nSUMMARY:Two days\r\nSTATUS:CANCELLED\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	feed, err := parseHolidayICS(ics, la)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 3 || feed[0].Date != "2027-01-04" || feed[1].Date != "2027-01-18" || feed[2].Date != "2027-02-01" || feed[0].Note != "Every other Monday" {
		t.Errorf("feed: %+v", feed)
	}
	if _, err := parseHolidayICS("<html>", la); err == nil {
		t.Error("HTML must be rejected")
	}
}
