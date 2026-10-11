// Command homework-hours serves the family control page on the CM4 and enforces its rules.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	listen := flag.String("listen", ":8443", "HTTPS listen address")
	dataDir := flag.String("data", "/etc/homework-hours", "directory for state, sessions and TLS files")
	aghURL := flag.String("adguard", "http://127.0.0.1:3000", "AdGuard Home base URL")
	aghSecret := flag.String("adguard-secret", "/etc/parentalcontrol/agh.secret", "file with user:password for the AdGuard API")
	bridge := flag.String("bridge", "br-lan", "bridge between the router and the wireless hub")
	wireless := flag.String("wireless-port", "eth0", "bridge port facing the wireless hub")
	gateway := flag.String("gateway", "192.168.0.1", "home router; answers reverse DNS for device names")
	tz := flag.String("tz", "America/Los_Angeles", "time zone of the rules")
	hosts := flag.String("hosts", "192.168.0.2,192.168.1.1,cm4-bridge,cm4-bridge.lan", "names and IPs the TLS certificate covers")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatalf("data dir: %v", err)
	}

	agh, err := newAdGuard(*aghURL, *aghSecret)
	if err != nil {
		log.Fatalf("adguard: %v", err)
	}
	store, err := openStore(filepath.Join(*dataDir, "state.json"))
	if err != nil {
		log.Fatalf("state: %v", err)
	}
	sessions, err := openSessions(filepath.Join(*dataDir, "sessions.json"))
	if err != nil {
		log.Fatalf("sessions: %v", err)
	}
	tlsCfg, err := loadOrCreateTLS(*dataDir, splitList(*hosts))
	if err != nil {
		log.Fatalf("tls: %v", err)
	}

	disc := openDiscovery(filepath.Join(*dataDir, "seen.json"), *bridge, *wireless, *gateway)
	go disc.run(context.Background(), func(mac string) bool {
		for _, d := range store.Snapshot().Devices {
			if d.MAC == mac {
				return true
			}
		}
		return false
	})

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatalf("time zone: %v", err)
	}
	enf := newEnforcer(store, disc, agh, loc, *bridge)
	go enf.run(context.Background())
	dsync := newDistrictSync(store, loc, enf.trigger)
	go dsync.run(context.Background())

	app := &App{store: store, sessions: sessions, agh: agh, disc: disc, enf: enf, dsync: dsync, limiter: newLimiter(5, 15*time.Minute), caPath: filepath.Join(*dataDir, "ca.crt")}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           app.routes(),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on https://%s", *listen)
	log.Fatal(srv.ListenAndServeTLS("", ""))
}
