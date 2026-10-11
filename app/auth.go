package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "hh_session"
	sessionTTL    = 30 * 24 * time.Hour
)

type session struct {
	User    string    `json:"user"`
	Expires time.Time `json:"expires"`
}

// Sessions survive restarts so parents are not logged out after a reboot.
type Sessions struct {
	mu   sync.Mutex
	path string
	m    map[string]session
}

func openSessions(path string) (*Sessions, error) {
	s := &Sessions{path: path, m: map[string]session{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.m); err != nil {
		return s, nil // a corrupt file only means everyone signs in again
	}
	return s, nil
}

func (s *Sessions) saveLocked() {
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.Expires) {
			delete(s.m, k)
		}
	}
	raw, _ := json.Marshal(s.m)
	_ = writeFileAtomic(s.path, raw, 0o600)
}

func (s *Sessions) create(user string) (string, time.Time) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token, exp := hex.EncodeToString(b), time.Now().Add(sessionTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[token] = session{User: user, Expires: exp}
	s.saveLocked()
	return token, exp
}

func (s *Sessions) lookup(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[token]
	if !ok || time.Now().After(v.Expires) {
		return "", false
	}
	return v.User, true
}

func (s *Sessions) remove(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, token)
	s.saveLocked()
}

// Limiter counts failed sign-ins per client IP. AdGuard's own limiter can't be used: every
// request we forward comes from 127.0.0.1, so one wrong guess would count against the whole house.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, fails: map[string][]time.Time{}}
}

func (l *Limiter) blockedFor(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-l.window)
	var keep []time.Time
	for _, t := range l.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.fails[ip] = keep
	if len(keep) < l.max {
		return 0
	}
	return time.Until(keep[0].Add(l.window))
}

func (l *Limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.fails[ip], time.Now())
}

func (l *Limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Send a name and password."})
		return
	}
	ip := clientIP(r)
	if wait := a.limiter.blockedFor(ip); wait > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many wrong passwords from this device. Try again in " + wait.Round(time.Minute).String() + "."})
		return
	}
	name := strings.TrimSpace(in.Name)
	ok := false
	if name != "" && in.Password != "" && name != a.agh.serviceUser {
		var err error
		if ok, err = a.agh.checkLogin(name, in.Password); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "AdGuard Home is not answering, so sign-in can't be checked right now."})
			return
		}
	}
	if !ok {
		a.limiter.fail(ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Wrong name or password. Use your AdGuard Home account."})
		return
	}
	a.limiter.reset(ip)
	token, exp := a.sessions.create(name)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: exp,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"user": name})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.sessions.remove(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (a *App) currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return a.sessions.lookup(c.Value)
}
