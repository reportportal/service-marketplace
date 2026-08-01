package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/reportportal/service-marketplace/internal/analytics"
	"github.com/reportportal/service-marketplace/internal/auth"
	"github.com/reportportal/service-marketplace/internal/catalogue"
	"github.com/reportportal/service-marketplace/internal/cdn"
	"github.com/reportportal/service-marketplace/internal/config"
	"github.com/reportportal/service-marketplace/internal/httpapi"
	"github.com/reportportal/service-marketplace/internal/license"
	"github.com/reportportal/service-marketplace/internal/lifecycle"
	"github.com/reportportal/service-marketplace/internal/publish"
	"github.com/reportportal/service-marketplace/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var store storage.ObjectStore
	var localStore *storage.LocalStore
	switch cfg.StorageType {
	case config.StorageLocal:
		localStore, err = storage.NewLocalStore(cfg.StorageLocalRoot, cfg.CDNBaseURL, cfg.StorageSigningSecret)
		if err != nil {
			log.Fatalf("local storage: %v", err)
		}
		store = localStore
	case config.StorageGCS:
		gcs, err := storage.NewGCSStore(ctx, cfg.GCSBucket, cfg.GCSPrivateBucket, cfg.CDNBaseURL, cfg.StorageSigningSecret)
		if err != nil {
			log.Fatalf("gcs storage: %v", err)
		}
		store = gcs
	default:
		log.Fatalf("unsupported storage type: %s", cfg.StorageType)
	}

	invalidator := cdn.Invalidator(&cdn.LogInvalidator{URLMap: cfg.CDNURLMap, Logger: log.Default()})
	if cfg.CDNURLMap != "" {
		invalidator = &cdn.GCPInvalidator{URLMap: cfg.CDNURLMap, Project: cfg.GCPProject, Logger: log.Default()}
	}

	pub := &publish.Service{Store: store, Invalidator: invalidator}
	cat := &catalogue.Service{Store: store}
	lc := &lifecycle.Service{Store: store, Invalidator: invalidator, Publisher: pub}
	lic := &license.Service{Store: store}
	ga := &analytics.GA4Client{MeasurementID: cfg.GA4MeasurementID, APISecret: cfg.GA4APISecret, Logger: log.Default()}

	denylist := auth.NewDenylist(store)
	sessions := auth.NewSessionManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTTTLSeconds, denylist)
	admin := auth.NewAdminAuthenticator(cfg.AdminLoginEnabled, cfg.AdminUsername, cfg.AdminPasswordHash, store)
	gh := &auth.GitHubOAuth{
		ClientID:     cfg.GitHubOAuthClientID,
		ClientSecret: cfg.GitHubOAuthClientSecret,
		Org:          cfg.GitHubOAuthOrg,
		AllowedTeam:  cfg.GitHubOAuthAllowedTeam,
		RedirectURL:  cfg.GitHubOAuthRedirectURL,
		Sessions:     sessions,
		States:       auth.NewOAuthStateStore(store),
	}
	oidc := &auth.PublishOIDCVerifier{
		Audience:       cfg.PublishOIDCAudience,
		AllowedSources: cfg.PublishOIDCAllowedSources,
	}

	orphanCleanupOwner := fmt.Sprintf("%s-%d", hostname(), os.Getpid())
	lc.StartOrphanCleanup(ctx, lifecycle.CleanupConfig{
		Enabled:     cfg.OrphanCleanupEnabled,
		DryRun:      cfg.OrphanCleanupDryRun,
		MinAge:      cfg.OrphanCleanupMinAge,
		RunInterval: cfg.OrphanCleanupRunInterval,
		LeaseTTL:    cfg.OrphanCleanupLeaseTTL,
	}, cfg.OrphanCleanupInterval, orphanCleanupOwner)

	srv := httpapi.NewServer(httpapi.Deps{
		Config:     cfg,
		Store:      store,
		LocalStore: localStore,
		Catalogue:  cat,
		Publish:    pub,
		Lifecycle:  lc,
		License:    lic,
		Analytics:  ga,
		Sessions:   sessions,
		AdminAuth:  admin,
		GitHub:     gh,
		OIDC:       oidc,
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("service-marketplace listening on %s (storage=%s)", cfg.HTTPAddr, store.Type())
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// hostname identifies this process for the orphan-cleanup lease's Owner
// field (diagnostics only -- the lease's coordination is by CAS generation,
// not by Owner value). Falls back to "unknown-host" rather than failing
// startup if os.Hostname is unavailable.
func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return h
}
