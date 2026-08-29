package handler

import (
	"context"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/joooostb/alltrails-to-gpx/internal/alltrails"
	"github.com/joooostb/alltrails-to-gpx/internal/cache"
)

// --- test doubles ---

type mockFetcher struct {
	json []byte
	slug string
	err  error
}

func (m *mockFetcher) FetchTrailJSON(_ context.Context, _ string, _ http.Header) ([]byte, string, error) {
	return m.json, m.slug, m.err
}

type mockConverter struct {
	gpx []byte
	err error
}

func (m *mockConverter) Convert(_ context.Context, _ []byte) ([]byte, error) {
	return m.gpx, m.err
}

// ctxCapturingConverter records whether the context it receives carries a
// deadline. It lets tests assert that the per-conversion timeout is applied.
type ctxCapturingConverter struct {
	gpx         []byte
	hadDeadline bool
	remaining   time.Duration
}

func (m *ctxCapturingConverter) Convert(ctx context.Context, _ []byte) ([]byte, error) {
	if dl, ok := ctx.Deadline(); ok {
		m.hadDeadline = true
		m.remaining = time.Until(dl)
	}
	return m.gpx, nil
}

// blockingConverter blocks until its context is cancelled, then returns the
// context error. It models a slow/hung alltrailsgpx subprocess.
type blockingConverter struct{}

func (blockingConverter) Convert(ctx context.Context, _ []byte) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// --- helpers ---

const testTemplates = `
{{define "layout"}}<html><body>layout</body></html>{{end}}
{{define "success"}}<div id="success">token={{.Token}} slug={{.Slug}}</div>{{end}}
{{define "error"}}<div id="error">{{.}}</div>{{end}}
`

func newTestHandler(t *testing.T, fetcher trailFetcher, conv gpxConverter, c *cache.Cache) *Handler {
	t.Helper()
	return newTestHandlerTimeout(t, fetcher, conv, c, 15*time.Second)
}

func newTestHandlerTimeout(t *testing.T, fetcher trailFetcher, conv gpxConverter, c *cache.Cache, convTimeout time.Duration) *Handler {
	t.Helper()
	tmpl := template.Must(template.New("").Parse(testTemplates))
	return New(tmpl, fetcher, conv, c, slog.New(slog.NewTextHandler(io.Discard, nil)), convTimeout)
}

func newCache(t *testing.T) *cache.Cache {
	t.Helper()
	return cache.New(time.Minute)
}

func postConvert(t *testing.T, h *Handler, trailURL string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"url": {trailURL}}
	req := httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.convert(rec, req)
	return rec
}

func postConvertRaw(t *testing.T, h *Handler, trailJSON, slug string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"trail_json": {trailJSON}, "slug": {slug}}
	req := httptest.NewRequest(http.MethodPost, "/convert-raw", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.convertRaw(rec, req)
	return rec
}

const validTrailURL = "https://www.alltrails.com/trail/us/california/half-dome"

// --- tests ---

func TestHealth(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{}, &mockConverter{}, newCache(t))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.health(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestIndex(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{}, &mockConverter{}, newCache(t))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	h.index(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "layout") {
		t.Errorf("body does not contain layout template output: %q", rec.Body.String())
	}
}

func TestIndex_NotFound(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{}, &mockConverter{}, newCache(t))
	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rec := httptest.NewRecorder()

	h.index(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestConvert_InvalidURL(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{}, &mockConverter{}, newCache(t))
	rec := postConvert(t, h, "https://not-alltrails.com/something")

	body := rec.Body.String()
	if !strings.Contains(body, `id="error"`) {
		t.Errorf("expected error fragment, got: %q", body)
	}
	if !strings.Contains(body, "valid AllTrails trail URL") {
		t.Errorf("expected validation message, got: %q", body)
	}
}

func TestConvert_TrailNotFound(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{err: alltrails.ErrTrailNotFound}, &mockConverter{}, newCache(t))
	rec := postConvert(t, h, "https://www.alltrails.com/trail/us/california/half-dome")

	body := rec.Body.String()
	if !strings.Contains(body, `id="error"`) {
		t.Errorf("expected error fragment, got: %q", body)
	}
	if !strings.Contains(body, "Trail not found") {
		t.Errorf("expected trail not found message, got: %q", body)
	}
}

func TestConvert_IDNotFound(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{err: alltrails.ErrIDNotFound}, &mockConverter{}, newCache(t))
	rec := postConvert(t, h, "https://www.alltrails.com/trail/us/california/half-dome")

	body := rec.Body.String()
	if !strings.Contains(body, "page format may have changed") {
		t.Errorf("expected ID extraction message, got: %q", body)
	}
}

func TestConvert_APIUnavailable(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{err: alltrails.ErrAPIUnavailable}, &mockConverter{}, newCache(t))
	rec := postConvert(t, h, "https://www.alltrails.com/trail/us/california/half-dome")

	body := rec.Body.String()
	if !strings.Contains(body, "Could not fetch trail data") {
		t.Errorf("expected API unavailable message, got: %q", body)
	}
}

func TestConvert_ConversionFailed(t *testing.T) {
	fetcher := &mockFetcher{json: []byte(`{}`), slug: "half-dome"}
	conv := &mockConverter{err: errors.New("bad json")}
	h := newTestHandler(t, fetcher, conv, newCache(t))
	rec := postConvert(t, h, "https://www.alltrails.com/trail/us/california/half-dome")

	body := rec.Body.String()
	if !strings.Contains(body, "GPX conversion failed") {
		t.Errorf("expected conversion failed message, got: %q", body)
	}
}

func TestConvert_Success(t *testing.T) {
	const gpxData = `<?xml version="1.0"?><gpx/>`
	fetcher := &mockFetcher{json: []byte(`{}`), slug: "half-dome"}
	conv := &mockConverter{gpx: []byte(gpxData)}
	h := newTestHandler(t, fetcher, conv, newCache(t))
	rec := postConvert(t, h, "https://www.alltrails.com/trail/us/california/half-dome")

	body := rec.Body.String()
	if !strings.Contains(body, `id="success"`) {
		t.Errorf("expected success fragment, got: %q", body)
	}
	if !strings.Contains(body, "half-dome") {
		t.Errorf("expected slug in success fragment, got: %q", body)
	}
}

func TestConvert_ServerBusy(t *testing.T) {
	fetcher := &mockFetcher{json: []byte(`{}`), slug: "half-dome"}
	conv := &mockConverter{gpx: []byte("<gpx/>")}
	h := newTestHandler(t, fetcher, conv, newCache(t))

	// Fill the semaphore so the next request sees a full server.
	for i := 0; i < cap(h.sem); i++ {
		h.sem <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(h.sem); i++ {
			<-h.sem
		}
	}()

	rec := postConvert(t, h, "https://www.alltrails.com/trail/us/california/half-dome")

	body := rec.Body.String()
	if !strings.Contains(body, "Server is busy") {
		t.Errorf("expected busy message, got: %q", body)
	}
}

// TestConvert_AppliesConversionTimeout asserts the conversion subprocess
// receives a context bounded by the configured timeout (not the bare,
// possibly-unbounded request context). Fails on the pre-fix code, where the
// converter was handed r.Context() directly and saw no deadline.
func TestConvert_AppliesConversionTimeout(t *testing.T) {
	fetcher := &mockFetcher{json: []byte(`{}`), slug: "half-dome"}
	conv := &ctxCapturingConverter{gpx: []byte("<gpx/>")}
	h := newTestHandlerTimeout(t, fetcher, conv, newCache(t), 15*time.Second)

	postConvert(t, h, validTrailURL)

	if !conv.hadDeadline {
		t.Fatal("conversion context had no deadline; CONVERSION_TIMEOUT not applied")
	}
	if conv.remaining <= 0 || conv.remaining > 15*time.Second {
		t.Errorf("deadline remaining = %v, want in (0, 15s]", conv.remaining)
	}
}

// TestConvertRaw_AppliesConversionTimeout is the same guarantee for the
// paste-JSON fallback path.
func TestConvertRaw_AppliesConversionTimeout(t *testing.T) {
	conv := &ctxCapturingConverter{gpx: []byte("<gpx/>")}
	h := newTestHandlerTimeout(t, &mockFetcher{}, conv, newCache(t), 15*time.Second)

	postConvertRaw(t, h, `{"trails":[]}`, "half-dome")

	if !conv.hadDeadline {
		t.Fatal("raw conversion context had no deadline; CONVERSION_TIMEOUT not applied")
	}
	if conv.remaining <= 0 || conv.remaining > 15*time.Second {
		t.Errorf("deadline remaining = %v, want in (0, 15s]", conv.remaining)
	}
}

// TestConvert_TimesOut verifies a slow/hung conversion is aborted at the
// deadline and the user sees the timeout message rather than the request
// hanging and holding a concurrency slot.
func TestConvert_TimesOut(t *testing.T) {
	fetcher := &mockFetcher{json: []byte(`{}`), slug: "half-dome"}
	h := newTestHandlerTimeout(t, fetcher, blockingConverter{}, newCache(t), 30*time.Millisecond)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- postConvert(t, h, validTrailURL) }()

	select {
	case rec := <-done:
		if !strings.Contains(rec.Body.String(), "Conversion timed out") {
			t.Errorf("expected timeout message, got: %q", rec.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("convert did not return; conversion timeout was not enforced")
	}
}

// TestConvert_NoTimeoutWhenDisabled verifies a zero timeout disables the
// per-conversion deadline (the parent context is used unchanged).
func TestConvert_NoTimeoutWhenDisabled(t *testing.T) {
	fetcher := &mockFetcher{json: []byte(`{}`), slug: "half-dome"}
	conv := &ctxCapturingConverter{gpx: []byte("<gpx/>")}
	h := newTestHandlerTimeout(t, fetcher, conv, newCache(t), 0)

	rec := postConvert(t, h, validTrailURL)

	if conv.hadDeadline {
		t.Error("expected no deadline when convTimeout is 0")
	}
	if !strings.Contains(rec.Body.String(), `id="success"`) {
		t.Errorf("expected success fragment, got: %q", rec.Body.String())
	}
}

func TestDownload_NotFound(t *testing.T) {
	h := newTestHandler(t, &mockFetcher{}, &mockConverter{}, newCache(t))
	req := httptest.NewRequest(http.MethodGet, "/gpx/badtoken", nil)
	req.SetPathValue("token", "badtoken")
	rec := httptest.NewRecorder()

	h.download(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestDownload_Success(t *testing.T) {
	const gpxData = `<?xml version="1.0"?><gpx/>`
	c := newCache(t)
	token, err := c.Put([]byte(gpxData), "half-dome")
	if err != nil {
		t.Fatalf("cache.Put() unexpected error: %v", err)
	}

	h := newTestHandler(t, &mockFetcher{}, &mockConverter{}, c)
	req := httptest.NewRequest(http.MethodGet, "/gpx/"+token, nil)
	req.SetPathValue("token", token)
	rec := httptest.NewRecorder()

	h.download(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gpx+xml" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/gpx+xml")
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "half-dome.gpx") {
		t.Errorf("Content-Disposition = %q, want filename containing half-dome.gpx",
			rec.Header().Get("Content-Disposition"))
	}
	if rec.Body.String() != gpxData {
		t.Errorf("body = %q, want %q", rec.Body.String(), gpxData)
	}
}

func TestSafeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"half-dome", "half-dome"},
		{"Half Dome", "half-dome"},
		{"trail_name", "trail-name"},
		{"trail!@#name", "trail---name"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := safeFilename(tt.input)
			if got != tt.want {
				t.Errorf("safeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
