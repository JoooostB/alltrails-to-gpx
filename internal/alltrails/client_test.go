package alltrails

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
)

// mockDoer lets tests control HTTP responses without a real network.
type mockDoer struct {
	fn func(*fhttp.Request) (*fhttp.Response, error)
}

func (m *mockDoer) Do(r *fhttp.Request) (*fhttp.Response, error) {
	return m.fn(r)
}

// newTestClient builds a Client whose requests are handled by fn.
func newTestClient(fn func(*fhttp.Request) (*fhttp.Response, error)) *Client {
	return &Client{
		http:     &mockDoer{fn: fn},
		baseHost: "www.alltrails.com",
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// fhttpResp builds a minimal fhttp.Response for test use.
func fhttpResp(code int, body string) *fhttp.Response {
	return &fhttp.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     fhttp.Header{},
	}
}

// pageHTML returns a minimal AllTrails-like HTML page containing the given
// trail slug and numeric ID, mimicking the Next.js App Router format where
// the ID appears in <link> preload URLs.
func pageHTML(slug string, id int) string {
	return fmt.Sprintf(`<html><head>
		<link rel="preload" as="image" imageSrcSet="https://www.alltrails.com/api/alltrails/v3/trails/%d/static_map?size=100x100 1x" />
	</head><body><div>%s</div></body></html>`, id, slug)
}

func TestFetchTrailJSON_Success(t *testing.T) {
	const trailURL = "https://www.alltrails.com/trail/us/california/half-dome"
	const trailID = 1234567
	const wantJSON = `{"trails":[{"id":1234567}]}`

	client := newTestClient(func(r *fhttp.Request) (*fhttp.Response, error) {
		switch r.URL.Path {
		case "/trail/us/california/half-dome":
			return fhttpResp(200, pageHTML("half-dome", trailID)), nil
		case fmt.Sprintf("/api/alltrails/v3/trails/%d", trailID):
			return fhttpResp(200, wantJSON), nil
		default:
			return fhttpResp(404, ""), nil
		}
	})

	data, slug, err := client.FetchTrailJSON(context.Background(), trailURL, nil)
	if err != nil {
		t.Fatalf("FetchTrailJSON() unexpected error: %v", err)
	}
	if slug != "half-dome" {
		t.Errorf("slug = %q, want %q", slug, "half-dome")
	}
	if string(data) != wantJSON {
		t.Errorf("data = %q, want %q", data, wantJSON)
	}
}

func TestFetchTrailJSON_PageNotFound(t *testing.T) {
	const trailURL = "https://www.alltrails.com/trail/us/california/half-dome"

	client := newTestClient(func(r *fhttp.Request) (*fhttp.Response, error) {
		return fhttpResp(404, ""), nil
	})

	_, _, err := client.FetchTrailJSON(context.Background(), trailURL, nil)
	if err != ErrTrailNotFound {
		t.Errorf("FetchTrailJSON() error = %v, want ErrTrailNotFound", err)
	}
}

func TestFetchTrailJSON_IDNotFound(t *testing.T) {
	const trailURL = "https://www.alltrails.com/trail/us/california/half-dome"

	client := newTestClient(func(r *fhttp.Request) (*fhttp.Response, error) {
		// Page exists but contains no trail ID.
		return fhttpResp(200, `<html><body><script>var x = {};</script></body></html>`), nil
	})

	_, _, err := client.FetchTrailJSON(context.Background(), trailURL, nil)
	if err != ErrIDNotFound {
		t.Errorf("FetchTrailJSON() error = %v, want ErrIDNotFound", err)
	}
}

func TestFetchTrailJSON_APIError(t *testing.T) {
	const trailURL = "https://www.alltrails.com/trail/us/california/half-dome"
	const trailID = 1234567

	client := newTestClient(func(r *fhttp.Request) (*fhttp.Response, error) {
		if r.URL.Path == "/trail/us/california/half-dome" {
			return fhttpResp(200, pageHTML("half-dome", trailID)), nil
		}
		return fhttpResp(500, ""), nil
	})

	_, _, err := client.FetchTrailJSON(context.Background(), trailURL, nil)
	if err != ErrAPIUnavailable {
		t.Errorf("FetchTrailJSON() error = %v, want ErrAPIUnavailable", err)
	}
}

func TestFetchTrailJSON_DataDomeCaptcha(t *testing.T) {
	const trailURL = "https://www.alltrails.com/trail/us/california/half-dome"
	const trailID = 1234567

	client := newTestClient(func(r *fhttp.Request) (*fhttp.Response, error) {
		if r.URL.Path == "/trail/us/california/half-dome" {
			return fhttpResp(200, pageHTML("half-dome", trailID)), nil
		}
		// API returns 403 with DataDome captcha-delivery.com body.
		return fhttpResp(403, `{"url":"https://captcha-delivery.com/c/?hash=abc"}`), nil
	})

	_, _, err := client.FetchTrailJSON(context.Background(), trailURL, nil)
	var captchaErr *ErrCaptchaChallenge
	if !errors.As(err, &captchaErr) {
		t.Errorf("FetchTrailJSON() error = %v, want *ErrCaptchaChallenge", err)
	}
	if captchaErr != nil && captchaErr.TrailURL != trailURL {
		t.Errorf("ErrCaptchaChallenge.TrailURL = %q, want %q", captchaErr.TrailURL, trailURL)
	}
}

func TestFetchTrailJSON_DataDomeCaptchaHTML(t *testing.T) {
	const trailURL = "https://www.alltrails.com/trail/us/california/half-dome"
	const trailID = 1234567

	// DataDome HTML JS-challenge — "captcha-delivery.com" may be cut off by
	// the snippet limit but "var dd={" is always in the first 512 bytes.
	const dataDomeHTML = `<html lang="nl"><head><title>alltrails.com</title><style>#cmsg{animation: A 1.5s;}@keyframes A{0%{opacity:0;}99%{opacity:0;}100%{opacity:1;}}</style></head><body style="margin:0"><p id="cmsg">Please enable JS and disable any ad blocker</p><script data-cfasync="false">var dd={'rt':'i','cid':'AHrlqAAAAAMAwl21yz1La_8A','hsh':'9D463B509A4C91FDFF39B265B3E2BC','b':1522184,'s':39492,'e':'abc123','qp':'','host':'geo.captcha-delivery.com'}</script></body></html>`

	client := newTestClient(func(r *fhttp.Request) (*fhttp.Response, error) {
		if r.URL.Path == "/trail/us/california/half-dome" {
			return fhttpResp(200, pageHTML("half-dome", trailID)), nil
		}
		return fhttpResp(403, dataDomeHTML), nil
	})

	_, _, err := client.FetchTrailJSON(context.Background(), trailURL, nil)
	var captchaErr *ErrCaptchaChallenge
	if !errors.As(err, &captchaErr) {
		t.Errorf("FetchTrailJSON() error = %v, want *ErrCaptchaChallenge", err)
	}
}

func TestFetchTrailJSON_ContextCancelled(t *testing.T) {
	const trailURL = "https://www.alltrails.com/trail/us/california/half-dome"

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := newTestClient(func(r *fhttp.Request) (*fhttp.Response, error) {
		return nil, r.Context().Err()
	})

	_, _, err := client.FetchTrailJSON(ctx, trailURL, nil)
	if err == nil {
		t.Error("FetchTrailJSON() expected error for cancelled context, got nil")
	}
}
