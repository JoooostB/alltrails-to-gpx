package alltrails

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	apiBaseURL    = "https://www.alltrails.com/api/alltrails/v3/trails/%s?detail=offline"
	fallbackUA    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	maxBody       = 5 * 1024 * 1024 // 5 MB cap on API responses
	maxErrBodyLog = 512              // bytes of error response body to log at DEBUG
)

// chromeHeaderOrder mirrors Chrome 124's canonical header send order for a
// top-level navigation request. Using fhttp.HeaderOrderKey enforces this at
// the TLS layer, making the request indistinguishable from a real browser.
var chromeHeaderOrder = []string{
	"cache-control",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"upgrade-insecure-requests",
	"user-agent",
	"accept",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-user",
	"sec-fetch-dest",
	"accept-encoding",
	"accept-language",
	"referer",
}

// chromePseudoHeaderOrder is the HTTP/2 pseudo-header order Chrome 124 sends.
// Setting fhttp.PHeaderOrderKey ensures the HEADERS frame matches Chrome's
// SETTINGS fingerprint.
var chromePseudoHeaderOrder = []string{":method", ":authority", ":scheme", ":path"}

// browserHeaders lists which incoming browser headers we forward to AllTrails.
var browserHeaders = []string{
	"User-Agent",
	"Accept-Language",
	"Sec-CH-UA",
	"Sec-CH-UA-Mobile",
	"Sec-CH-UA-Platform",
}

// doer is satisfied by tlsclient.HttpClient and by test mocks.
type doer interface {
	Do(*fhttp.Request) (*fhttp.Response, error)
}

// Client fetches trail data from AllTrails using a Chrome-impersonating TLS
// client to defeat JA3/JA4 and HTTP/2 SETTINGS fingerprint bot-detection.
type Client struct {
	http     doer
	baseHost string // production = "www.alltrails.com"; overridable in tests
	log      *slog.Logger
}

// NewClient creates a Client backed by a TLS client impersonating Chrome 124.
// Returns an error if the underlying TLS client cannot be initialised.
func NewClient(timeout time.Duration, log *slog.Logger) (*Client, error) {
	opts := []tlsclient.HttpClientOption{
		tlsclient.WithTimeoutSeconds(int(timeout.Seconds())),
		tlsclient.WithClientProfile(profiles.Chrome_124),
	}
	c, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), opts...)
	if err != nil {
		return nil, fmt.Errorf("tls-client init: %w", err)
	}
	return &Client{
		http:     c,
		baseHost: "www.alltrails.com",
		log:      log,
	}, nil
}

// FetchTrailJSON fetches the AllTrails API JSON for the given trail URL.
// It performs two requests: one to the trail page to extract the numeric ID,
// and one to the AllTrails v3 API.
//
// passthrough contains browser headers forwarded from the user's request
// (User-Agent, Accept-Language, Sec-CH-UA family). These layer on top of
// the Chrome-accurate baseline headers for maximum authenticity.
// Pass nil to use built-in defaults.
func (c *Client) FetchTrailJSON(ctx context.Context, trailURL string, passthrough http.Header) ([]byte, string, error) {
	slug := SlugFromURL(trailURL)

	// Step 1: fetch the trail page HTML and extract the numeric trail ID.
	pageBody, err := c.get(ctx, trailURL, trailURL, passthrough)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = pageBody.Close() }()

	trailID, err := ExtractTrailID(pageBody, slug)
	if err != nil {
		return nil, "", err
	}

	// Step 2: call the AllTrails v3 API.
	apiURL := fmt.Sprintf(apiBaseURL, trailID)
	apiBody, err := c.get(ctx, apiURL, trailURL, passthrough)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = apiBody.Close() }()

	data, err := io.ReadAll(io.LimitReader(apiBody, maxBody))
	if err != nil {
		return nil, "", ErrAPIUnavailable
	}

	return data, slug, nil
}

func (c *Client) get(ctx context.Context, targetURL, referer string, passthrough http.Header) (io.ReadCloser, error) {
	// Host whitelist — belt-and-suspenders SSRF guard.
	u, err := url.Parse(targetURL)
	if err != nil || u.Host != c.baseHost {
		return nil, ErrInvalidURL
	}

	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, targetURL, nil)
	if err != nil {
		return nil, ErrAPIUnavailable
	}

	// Baseline Chrome 124 headers for a top-level navigation request.
	req.Header = fhttp.Header{
		"Cache-Control":             {"max-age=0"},
		"Sec-CH-UA":                 {`"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"`},
		"Sec-CH-UA-Mobile":          {"?0"},
		"Sec-CH-UA-Platform":        {`"macOS"`},
		"Upgrade-Insecure-Requests": {"1"},
		"User-Agent":                {fallbackUA},
		"Accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
		"Sec-Fetch-Site":            {"none"},
		"Sec-Fetch-Mode":            {"navigate"},
		"Sec-Fetch-User":            {"?1"},
		"Sec-Fetch-Dest":            {"document"},
		"Accept-Encoding":           {"gzip, deflate, br, zstd"},
		"Accept-Language":           {"en-US,en;q=0.9"},
		"Referer":                   {referer},
		// Enforce Chrome header send order (JA3/HTTP2 fingerprint).
		fhttp.HeaderOrderKey:  chromeHeaderOrder,
		fhttp.PHeaderOrderKey: chromePseudoHeaderOrder,
	}

	// Override with actual browser headers where the user's browser sent them.
	for _, name := range browserHeaders {
		if v := passthrough.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}

	c.log.Debug("alltrails request", "url", targetURL, "user_agent", req.Header.Get("User-Agent"))

	resp, err := c.http.Do(req)
	if err != nil {
		c.log.Debug("alltrails request failed", "url", targetURL, "err", err)
		return nil, ErrAPIUnavailable
	}

	c.log.Debug("alltrails response", "url", targetURL, "status", resp.StatusCode)

	if resp.StatusCode == fhttp.StatusNotFound {
		_ = resp.Body.Close()
		return nil, ErrTrailNotFound
	}
	if resp.StatusCode != fhttp.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyLog))
		_ = resp.Body.Close()
		c.log.Debug("alltrails error response",
			"url", targetURL,
			"status", resp.StatusCode,
			"body", string(snippet),
		)
		// DataDome returns 403 with either:
		//   - a JSON body containing a captcha-delivery.com URL, or
		//   - an HTML JS-challenge page with a "var dd={" script (also DataDome).
		// The HTML variant can truncate before "captcha-delivery.com" appears,
		// so we match both signatures.
		if resp.StatusCode == fhttp.StatusForbidden &&
			(strings.Contains(string(snippet), "captcha-delivery.com") ||
				strings.Contains(string(snippet), "var dd={")) {
			c.log.Info("DataDome bot-detection triggered", "url", targetURL)
			return nil, &ErrCaptchaChallenge{TrailURL: referer}
		}
		return nil, ErrAPIUnavailable
	}

	return resp.Body, nil
}
