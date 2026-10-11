package main

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// parseHolidayICS reads the district's "Holidays and Closures" iCalendar feed and returns one
// item per run of no-school days. Multi-day closures are recurring all-day events (e.g. Winter
// Break = every weekday until 1/1), so each event's RRULE/EXDATE is expanded into days, and days
// at most 3 apart (a weekend) are joined into one range. Only what the district feed uses is
// supported: DAILY/WEEKLY rules with INTERVAL, COUNT, UNTIL and BYDAY.
func parseHolidayICS(raw string, loc *time.Location) ([]FeedItem, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n ", "")
	raw = strings.ReplaceAll(raw, "\n\t", "")
	if !strings.HasPrefix(strings.TrimSpace(raw), "BEGIN:VCALENDAR") {
		return nil, errors.New("not an iCalendar file")
	}
	var out []FeedItem
	var ev map[string][]string
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case line == "BEGIN:VEVENT":
			ev = map[string][]string{}
		case line == "END:VEVENT":
			if ev != nil {
				out = append(out, expandEvent(ev, loc)...)
			}
			ev = nil
		case ev != nil:
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			key, params, _ := strings.Cut(name, ";")
			ev[key] = append(ev[key], value)
			if key == "DTSTART" || key == "DTEND" || key == "EXDATE" {
				ev[key+";"] = append(ev[key+";"], params)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the calendar has no events")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// icsDay turns a DATE or DATE-TIME value into the local calendar day.
func icsDay(value, params string, loc *time.Location) (time.Time, bool) {
	if len(value) < 8 {
		return time.Time{}, false
	}
	if len(value) >= 15 && value[8] == 'T' {
		zone := loc
		if strings.HasSuffix(value, "Z") {
			zone = time.UTC
		} else if _, tz, ok := strings.Cut(params, "TZID="); ok {
			if l, err := time.LoadLocation(strings.Split(tz, ";")[0]); err == nil {
				zone = l
			}
		}
		t, err := time.ParseInLocation("20060102T150405", strings.TrimSuffix(value[:15], "Z"), zone)
		if err != nil {
			return time.Time{}, false
		}
		t = t.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
	}
	t, err := time.Parse("20060102", value[:8])
	return t, err == nil
}

func icsText(s string) string {
	r := strings.NewReplacer(`\,`, ",", `\;`, ";", `\n`, " ", `\N`, " ", `\\`, `\`)
	return strings.Join(strings.Fields(r.Replace(s)), " ")
}

var icsWeekdays = map[string]time.Weekday{"SU": 0, "MO": 1, "TU": 2, "WE": 3, "TH": 4, "FR": 5, "SA": 6}

func expandEvent(ev map[string][]string, loc *time.Location) []FeedItem {
	first := func(k string) string {
		if v := ev[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	if strings.EqualFold(first("STATUS"), "CANCELLED") {
		return nil
	}
	start, ok := icsDay(first("DTSTART"), first("DTSTART;"), loc)
	note := icsText(first("SUMMARY"))
	if !ok || note == "" {
		return nil
	}
	span := 1
	if end, ok := icsDay(first("DTEND"), first("DTEND;"), loc); ok && end.After(start) {
		span = int(end.Sub(start).Hours()/24 + 0.5)
	}

	// Occurrence start days
	starts := []time.Time{start}
	if rule := first("RRULE"); rule != "" {
		starts = expandRule(start, rule)
	}
	excluded := map[string]bool{}
	for i, v := range ev["EXDATE"] {
		params := ""
		if i < len(ev["EXDATE;"]) {
			params = ev["EXDATE;"][i]
		}
		for _, d := range strings.Split(v, ",") {
			if t, ok := icsDay(d, params, loc); ok {
				excluded[t.Format("2006-01-02")] = true
			}
		}
	}
	days := map[string]bool{}
	for _, s := range starts {
		if excluded[s.Format("2006-01-02")] {
			continue
		}
		for i := 0; i < span && i < 366; i++ {
			days[s.AddDate(0, 0, i).Format("2006-01-02")] = true
		}
	}
	list := make([]string, 0, len(days))
	for d := range days {
		list = append(list, d)
	}
	sort.Strings(list)

	var out []FeedItem
	for _, d := range list {
		if n := len(out); n > 0 && dayGap(out[n-1].To, d) <= 3 {
			out[n-1].To = d
			continue
		}
		out = append(out, FeedItem{Date: d, To: d, Note: note})
	}
	return out
}

func dayGap(a, b string) int {
	ta, _ := time.Parse("2006-01-02", a)
	tb, _ := time.Parse("2006-01-02", b)
	return int(tb.Sub(ta).Hours() / 24)
}

// expandRule lists the start days of a DAILY or WEEKLY rule (at most a year of them). UNTIL is
// compared by its date: the district writes the last day as "<day>T000000Z", and an all-day
// occurrence on that day is meant to be included. Other frequencies yield just the first day.
func expandRule(start time.Time, rule string) []time.Time {
	parts := map[string]string{}
	for _, p := range strings.Split(rule, ";") {
		if k, v, ok := strings.Cut(p, "="); ok {
			parts[strings.ToUpper(k)] = v
		}
	}
	freq := strings.ToUpper(parts["FREQ"])
	if freq != "DAILY" && freq != "WEEKLY" {
		return []time.Time{start}
	}
	interval, _ := strconv.Atoi(parts["INTERVAL"])
	if interval < 1 {
		interval = 1
	}
	count, _ := strconv.Atoi(parts["COUNT"])
	until := start.AddDate(1, 0, 0)
	if u := parts["UNTIL"]; len(u) >= 8 {
		if t, err := time.Parse("20060102", u[:8]); err == nil && t.Before(until) {
			until = t
		}
	}
	byDay := map[time.Weekday]bool{}
	for _, d := range strings.Split(parts["BYDAY"], ",") {
		if wd, ok := icsWeekdays[strings.ToUpper(strings.TrimSpace(d))]; ok {
			byDay[wd] = true
		}
	}
	if freq == "WEEKLY" && len(byDay) == 0 {
		byDay[start.Weekday()] = true
	}
	// Weeks start on Monday (WKST default)
	weekStart := func(t time.Time) time.Time { return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)) }
	firstWeek := weekStart(start)

	var out []time.Time
	for d, i := start, 0; !d.After(until); d, i = d.AddDate(0, 0, 1), i+1 {
		if freq == "DAILY" && i%interval != 0 {
			continue
		}
		if freq == "WEEKLY" && (int(weekStart(d).Sub(firstWeek).Hours()/24+0.5)/7)%interval != 0 {
			continue
		}
		if len(byDay) > 0 && !byDay[d.Weekday()] {
			continue
		}
		out = append(out, d)
		if count > 0 && len(out) >= count {
			break
		}
	}
	return out
}
