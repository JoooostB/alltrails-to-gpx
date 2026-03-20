package main

import (
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joooostb/alltrails-to-gpx/internal/alltrails"
	"github.com/joooostb/alltrails-to-gpx/internal/assets"
	"github.com/joooostb/alltrails-to-gpx/internal/cache"
	"github.com/joooostb/alltrails-to-gpx/internal/config"
	"github.com/joooostb/alltrails-to-gpx/internal/converter"
	"github.com/joooostb/alltrails-to-gpx/internal/handler"
)

func main() {
	cfg := config.Load()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	tmpl, err := template.New("").Funcs(template.FuncMap{
		// urlSlug extracts the last path segment from an AllTrails URL, used to
		// pre-populate the slug field in the manual-mode captcha form.
		"urlSlug": alltrails.SlugFromURL,
	}).ParseFS(assets.FS, "templates/*.html")
	if err != nil {
		log.Error("failed to parse templates", "err", err)
		os.Exit(1)
	}

	staticFS, err := fs.Sub(assets.FS, "static")
	if err != nil {
		log.Error("failed to load static assets", "err", err)
		os.Exit(1)
	}

	atClient, err := alltrails.NewClient(cfg.HTTPRequestTimeout, log)
	if err != nil {
		log.Error("alltrails client init failed", "err", err)
		os.Exit(1)
	}

	conv, err := converter.New(cfg.AlltrailsgpxBin, log)
	if err != nil {
		log.Error("converter init failed", "err", err)
		os.Exit(1)
	}

	gpxCache := cache.New(cfg.CacheTTL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gpxCache.StartSweep(ctx, cfg.CacheSweepInterval)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	h := handler.New(tmpl, atClient, conv, gpxCache, log)
	h.RegisterRoutes(mux)

	writeTimeout := cfg.HTTPRequestTimeout + cfg.ConversionTimeout + 10*time.Second
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      securityHeaders(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: writeTimeout,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
}

// securityHeaders adds minimal defensive HTTP response headers to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
