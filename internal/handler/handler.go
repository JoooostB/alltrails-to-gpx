package handler

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/joooostb/alltrails-to-gpx/internal/alltrails"
	"github.com/joooostb/alltrails-to-gpx/internal/cache"
)

// trailFetcher is satisfied by *alltrails.Client.
type trailFetcher interface {
	FetchTrailJSON(ctx context.Context, trailURL string, passthrough http.Header) ([]byte, string, error)
}

// gpxConverter is satisfied by *converter.Converter.
type gpxConverter interface {
	Convert(ctx context.Context, trailJSON []byte) ([]byte, error)
}

const maxConcurrent = 5

// Handler holds the dependencies for all HTTP handlers.
type Handler struct {
	tmpl      *template.Template
	client    trailFetcher
	converter gpxConverter
	cache     *cache.Cache
	log       *slog.Logger
	sem       chan struct{}
}

// New constructs a Handler. tmpl must contain the named templates "layout",
// "success", and "error".
func New(
	tmpl *template.Template,
	client trailFetcher,
	conv gpxConverter,
	c *cache.Cache,
	log *slog.Logger,
) *Handler {
	return &Handler{
		tmpl:      tmpl,
		client:    client,
		converter: conv,
		cache:     c,
		log:       log,
		sem:       make(chan struct{}, maxConcurrent),
	}
}

// RegisterRoutes attaches all routes to mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.index)
	mux.HandleFunc("POST /convert", h.convert)
	mux.HandleFunc("POST /convert-raw", h.convertRaw)
	mux.HandleFunc("GET /gpx/{token}", h.download)
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /robots.txt", h.robotsTxt)
	mux.HandleFunc("GET /sitemap.xml", h.sitemapXML)
}

// --- handlers ---

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := h.tmpl.ExecuteTemplate(w, "layout", nil); err != nil {
		h.log.Error("template execute failed", "err", err)
	}
}

func (h *Handler) convert(w http.ResponseWriter, r *http.Request) {
	// Concurrency cap — return busy error instead of queuing.
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		h.renderError(w, "Server is busy. Please try again in a moment.")
		return
	}

	rawURL := strings.TrimSpace(r.FormValue("url"))
	if err := alltrails.ValidateURL(rawURL); err != nil {
		h.renderError(w, "Please enter a valid AllTrails trail URL (e.g. https://www.alltrails.com/trail/...)")
		return
	}

	ctx := r.Context()

	trailJSON, slug, err := h.client.FetchTrailJSON(ctx, rawURL, browserPassthrough(r))
	if err != nil {
		h.log.Error("fetch trail JSON failed", "url", rawURL, "err", err)
		var captchaErr *alltrails.ErrCaptchaChallenge
		if errors.As(err, &captchaErr) {
			h.renderCaptchaGuide(w, captchaErr.TrailURL)
			return
		}
		h.renderError(w, fetchErrMessage(err))
		return
	}

	gpx, err := h.converter.Convert(ctx, trailJSON)
	if err != nil {
		if isContextErr(ctx) {
			h.renderError(w, "Conversion timed out. Please try again.")
		} else {
			h.log.Error("GPX conversion failed", "slug", slug, "err", err)
			h.renderError(w, "GPX conversion failed. The trail data may be in an unsupported format.")
		}
		return
	}

	token, err := h.cache.Put(gpx, slug)
	if err != nil {
		h.log.Error("cache put failed", "err", err)
		h.renderError(w, "Server is busy. Please try again in a moment.")
		return
	}

	h.renderSuccess(w, token, slug)
}

// convertRaw accepts AllTrails API JSON pasted directly by the user and
// converts it to GPX without any server-side AllTrails requests. This is the
// fallback path when bot-detection blocks the automatic flow.
func (h *Handler) convertRaw(w http.ResponseWriter, r *http.Request) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		h.renderError(w, "Server is busy. Please try again in a moment.")
		return
	}

	rawJSON := strings.TrimSpace(r.FormValue("trail_json"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	if rawJSON == "" {
		h.renderError(w, "Please paste the trail JSON before converting.")
		return
	}
	if slug == "" {
		slug = "trail"
	}

	ctx := r.Context()

	gpx, err := h.converter.Convert(ctx, []byte(rawJSON))
	if err != nil {
		if isContextErr(ctx) {
			h.renderError(w, "Conversion timed out. Please try again.")
		} else {
			h.log.Error("GPX conversion failed (raw)", "slug", slug, "err", err)
			h.renderError(w, "GPX conversion failed. Make sure you pasted the full JSON response from the AllTrails API.")
		}
		return
	}

	token, err := h.cache.Put(gpx, slug)
	if err != nil {
		h.log.Error("cache put failed", "err", err)
		h.renderError(w, "Server is busy. Please try again in a moment.")
		return
	}

	h.renderSuccess(w, token, slug)
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	entry, err := h.cache.Get(token)
	if err != nil {
		http.Error(w, "Download link expired. Please convert the trail again.", http.StatusNotFound)
		return
	}

	filename := safeFilename(entry.TrailSlug) + ".gpx"
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})

	w.Header().Set("Content-Type", "application/gpx+xml")
	w.Header().Set("Content-Disposition", disposition)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(entry.GPXBytes)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) robotsTxt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nDisallow: /gpx/\nDisallow: /convert\nDisallow: /convert-raw\n\nSitemap: https://alltrailstogpx.io/sitemap.xml\n"))
}

func (h *Handler) sitemapXML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://alltrailstogpx.io/</loc>
    <changefreq>monthly</changefreq>
    <priority>1.0</priority>
  </url>
</urlset>`))
}

// --- template helpers ---

type successData struct {
	Token string
	Slug  string
}

func (h *Handler) renderSuccess(w http.ResponseWriter, token, slug string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "success", successData{Token: token, Slug: slug}); err != nil {
		h.log.Error("success template failed", "err", err)
	}
}

func (h *Handler) renderError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "error", msg); err != nil {
		h.log.Error("error template failed", "err", err)
	}
}

func (h *Handler) renderCaptchaGuide(w http.ResponseWriter, trailURL string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "captcha", trailURL); err != nil {
		h.log.Error("captcha template failed", "err", err)
	}
}

// --- helpers ---

var unsafeChars = regexp.MustCompile(`[^a-z0-9\-]`)

// safeFilename converts a trail slug into a safe filename component.
func safeFilename(slug string) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			return unicode.ToLower(r)
		}
		return '-'
	}, slug)
	return unsafeChars.ReplaceAllString(s, "-")
}

func fetchErrMessage(err error) string {
	switch err {
	case alltrails.ErrTrailNotFound:
		return "Trail not found. Please check the URL."
	case alltrails.ErrIDNotFound:
		return "Could not read trail data. The page format may have changed."
	default:
		return "Could not fetch trail data from AllTrails. Please try again later."
	}
}

func isContextErr(ctx context.Context) bool {
	return ctx.Err() != nil
}

// passthroughHeaders lists the incoming request headers forwarded to AllTrails.
// These make outbound requests look like they originate from a real browser.
var passthroughHeaders = []string{
	"User-Agent",
	"Accept-Language",
	"Sec-CH-UA",
	"Sec-CH-UA-Mobile",
	"Sec-CH-UA-Platform",
}

// browserPassthrough extracts the whitelisted browser headers from the
// incoming request to be forwarded to AllTrails.
func browserPassthrough(r *http.Request) http.Header {
	out := make(http.Header)
	for _, name := range passthroughHeaders {
		if v := r.Header.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return out
}
