package main

import (
	"embed"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

//go:embed web
var webFS embed.FS

type App struct {
	store    *Store
	sessions *Sessions
	agh      *AdGuard
	disc     *Discovery
	enf      *Enforcer
	dsync    *DistrictSync
	catalog  catalogCache
	limiter  *Limiter
	caPath   string
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", a.servePage("web/login.html"))
	mux.HandleFunc("GET /ca.crt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="homework-hours-ca.crt"`)
		http.ServeFile(w, r, a.caPath)
	})
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/me", a.authed(func(w http.ResponseWriter, r *http.Request, user string) {
		writeJSON(w, http.StatusOK, map[string]string{"user": user})
	}))
	mux.HandleFunc("GET /api/state", a.authed(func(w http.ResponseWriter, r *http.Request, user string) {
		writeJSON(w, http.StatusOK, a.store.Snapshot())
	}))
	mux.HandleFunc("PUT /api/state", a.authed(a.handlePutState))
	mux.HandleFunc("GET /api/devices", a.authed(func(w http.ResponseWriter, r *http.Request, user string) {
		writeJSON(w, http.StatusOK, map[string]any{"devices": a.disc.list(a.store.Snapshot().Devices)})
	}))
	mux.HandleFunc("GET /api/catalog", a.authed(a.handleCatalog))
	mux.HandleFunc("GET /api/sync", a.authed(func(w http.ResponseWriter, r *http.Request, user string) {
		writeJSON(w, http.StatusOK, a.enf.Status())
	}))
	mux.HandleFunc("GET /api/activity", a.authed(a.handleActivity))
	mux.HandleFunc("POST /api/district/sync", a.authed(func(w http.ResponseWriter, r *http.Request, user string) {
		st, err := a.dsync.Sync(r.Context())
		if err != nil && st.Version == 0 {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save on the CM4."})
			return
		}
		out := map[string]any{"state": st}
		if err != nil {
			out["error"] = err.Error()
		}
		log.Printf("district sync by %s: %v", user, err)
		writeJSON(w, http.StatusOK, out)
	}))
	mux.HandleFunc("POST /api/district/dismiss", a.authed(func(w http.ResponseWriter, r *http.Request, user string) {
		st, err := a.store.UpdateDistrict(func(d *District) { d.Notices = []DistrictNotice{} })
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save on the CM4."})
			return
		}
		writeJSON(w, http.StatusOK, st)
	}))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.currentUser(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		a.servePage("web/index.html")(w, r)
	})
	return securityHeaders(mux)
}

func (a *App) servePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := webFS.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	}
}

// authed requires a session. State-changing requests must also carry X-HH: 1, a custom header
// a cross-site form can't send, on top of the SameSite=Strict cookie.
func (a *App) authed(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := a.currentUser(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Please sign in again."})
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-HH") != "1" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Missing request header."})
			return
		}
		h(w, r, user)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// handlePutState replaces the whole configuration. The page sends the version it loaded; if
// another parent saved in between, the answer is 409 with the current state to reload from.
func (a *App) handlePutState(w http.ResponseWriter, r *http.Request, user string) {
	var next State
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Could not read the change."})
		return
	}
	if err := validateState(&next, time.Now()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	saved, err := a.store.Replace(next.Version, next, a.disc.known)
	if errors.Is(err, ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Someone else changed the settings. The page reloaded them; please redo your change.", "state": a.store.Snapshot()})
		return
	} else if err != nil {
		log.Printf("save state: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save on the CM4."})
		return
	}
	log.Printf("state v%d saved by %s", saved.Version, user)
	a.enf.trigger()
	writeJSON(w, http.StatusOK, saved)
}
