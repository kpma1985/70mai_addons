package main

import (
	"context"
	"embed"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"addon_installer/internal/api"
	"addon_installer/internal/config"
	"addon_installer/internal/version"
)

//go:embed web/*
var webFS embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1:8765", "HTTP bind address")
	showVersion := flag.Bool("version", false, "Version ausgeben und beenden")
	apiToken := flag.String("api-token", strings.TrimSpace(os.Getenv("X800_ADDON_API_TOKEN")), "optional: X-Addon-Token Header für alle /api/* (Schutz bei LAN-Bind)")
	cfgPath := flag.String("config", "", "optional: Pfad zu installer.json")
	flag.Parse()
	if *showVersion {
		log.Printf("%s %s", version.AppName, version.Version)
		return
	}

	if api.ListenExposesLAN(*listen) && strings.TrimSpace(*apiToken) == "" {
		log.Printf("Sicherheit: %q ist nicht nur localhost — ohne -api-token ist die API im Netz erreichbar. Empfehlung: 127.0.0.1:PORT oder -api-token setzen.", *listen)
	}

	store := &config.Store{Path: *cfgPath}
	saved, err := store.Load()
	if err != nil {
		log.Printf("config laden: %v — Defaults", err)
		saved = config.Default()
	}
	var savedMu sync.RWMutex

	currentConfig := func() config.Config {
		savedMu.RLock()
		defer savedMu.RUnlock()
		return saved
	}

	saveCfg := func(c config.Config) error {
		if err := store.Save(c); err != nil {
			return err
		}
		savedMu.Lock()
		saved = c
		savedMu.Unlock()
		return nil
	}

	srv := &api.Server{
		CurrentConfig: currentConfig,
		SaveCfg:       saveCfg,
		Store:         store,
		WebFS:         webFS,
		WpalibFS:      wpalibFS,
	}

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	log.Printf("%s %s — http://%s\n", version.AppName, version.Version, *listen)
	if strings.TrimSpace(*apiToken) != "" {
		log.Printf("API-Token aktiv: alle /api/* Aufrufe benötigen Header X-Addon-Token.\n")
	}

	httpSrv := &http.Server{
		Addr:         *listen,
		Handler:      api.Wrap(*apiToken, mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go func() {
		<-stop
		log.Println("Signal empfangen, fahre herunter...")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Printf("Shutdown-Fehler: %v", err)
		}
	}()

	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Println("Server gestoppt.")
}
