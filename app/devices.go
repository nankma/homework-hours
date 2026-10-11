package main

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// catalogCache keeps AdGuard's service catalog for a day; it only changes with AdGuard updates.
type catalogCache struct {
	mu       sync.Mutex
	services []Service
	fetched  time.Time
}

func (a *App) handleCatalog(w http.ResponseWriter, r *http.Request, user string) {
	c := &a.catalog
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.services == nil || time.Since(c.fetched) > 24*time.Hour {
		svcs, err := a.agh.services()
		if err != nil {
			log.Printf("catalog: %v", err)
			if c.services == nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "AdGuard Home is not answering."})
				return
			}
		} else {
			c.services, c.fetched = svcs, time.Now()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": c.services})
}

// Activity ranges the page offers, in minutes (30 min … 1 week, the query log keeps 7 days).
var activityRanges = map[int]bool{30: true, 60: true, 120: true, 240: true, 1440: true, 10080: true}

// handleActivity returns one device's DNS lookups per host name over the chosen range.
func (a *App) handleActivity(w http.ResponseWriter, r *http.Request, user string) {
	mac := strings.ToLower(r.URL.Query().Get("mac"))
	minutes, _ := strconv.Atoi(r.URL.Query().Get("range"))
	if !reMAC.MatchString(mac) || !activityRanges[minutes] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Bad device or time range."})
		return
	}
	ip := a.disc.ipOf(mac)
	if ip == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ip": "", "domains": []DomainHits{}, "truncated": false})
		return
	}
	hits, truncated, err := a.agh.activity(ip, time.Now().Add(-time.Duration(minutes)*time.Minute), 20000)
	if err != nil {
		log.Printf("activity %s: %v", ip, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Could not read the AdGuard query log."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": ip, "domains": hits, "truncated": truncated})
}
