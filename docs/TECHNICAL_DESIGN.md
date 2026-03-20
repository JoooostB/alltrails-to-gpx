# Technical Design: AllTrails to GPX

> Companion to `FUNCTIONAL_DESIGN.md`. Covers implementation decisions,
> package layout, interfaces, and deployment artefacts.

---

## 1. Technology Choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Go 1.22+ | Route patterns (`POST /path`, `{token}`) built into `net/http` |
| HTTP router | `net/http` (stdlib) | No external dependency; 1.22 patterns sufficient for this app |
| Templating | `html/template` + `//go:embed` | Stdlib, auto-escaping XSS protection, binary embedded at build time |
| Logging | `log/slog` | Stdlib since Go 1.21; structured JSON output; zero dependencies |
| HTML parsing | `golang.org/x/net/html` | Safe DOM traversal for extracting the trail ID from AllTrails pages |
| UUID tokens | `crypto/rand` (stdlib) | No external UUID library needed for simple token generation |
| Testing | `testing` + `net/http/httptest` | Stdlib; sufficient for unit + handler tests |

**External Go dependencies (go.mod):**
- `golang.org/x/net` — for `html` subpackage (HTML parsing)

All other packages are from the standard library.

---

## 2. Project Layout

```
alltrails-to-gpx/
├── cmd/
│   └── server/
│       └── main.go            # Wire-up: load config, build deps, start server
├── internal/
│   ├── alltrails/
│   │   ├── client.go          # FetchTrailJSON: page fetch + API call
│   │   ├── client_test.go
│   │   ├── extract.go         # ExtractTrailID: parse HTML for numeric ID
│   │   └── extract_test.go
│   ├── converter/
│   │   ├── converter.go       # Run alltrailsgpx as subprocess
│   │   └── converter_test.go
│   ├── cache/
│   │   ├── cache.go           # In-memory GPX store with TTL
│   │   └── cache_test.go
│   ├── handler/
│   │   ├── handler.go         # HTTP handlers (convert, download, health)
│   │   └── handler_test.go
│   └── config/
│       └── config.go          # Load config from environment variables
├── templates/
│   ├── layout.html            # Full page (served by GET /)
│   └── fragments.html         # HTMX partials: success, error
├── Dockerfile
├── k8s/
│   ├── deployment.yaml
│   └── service.yaml
├── go.mod
└── go.sum
```

---

## 3. Configuration (`internal/config`)

Loaded once at startup from environment variables; passed by value.

```go
type Config struct {
    Port               string
    AlltrailsgpxBin    string
    HTTPRequestTimeout time.Duration
    ConversionTimeout  time.Duration
    CacheTTL           time.Duration
    CacheSweepInterval time.Duration
    LogLevel           slog.Level
}

func Load() Config
```

Defaults (applied when the variable is unset or empty):

| Variable | Default |
|---|---|
| `PORT` | `8080` |
| `ALLTRAILSGPX_BIN` | `alltrailsgpx` |
| `HTTP_REQUEST_TIMEOUT` | `30s` |
| `CONVERSION_TIMEOUT` | `15s` |
| `CACHE_TTL` | `5m` |
| `LOG_LEVEL` | `info` |

---

## 4. AllTrails Package (`internal/alltrails`)

### 4.1 URL Validation

AllTrails trail URLs follow one of these patterns:
```
https://www.alltrails.com/trail/{country}/{region}/{slug}
https://www.alltrails.com/en-gb/trail/{country}/{region}/{slug}   (localised)
```

Compiled regex at package init:
```go
var trailURLPattern = regexp.MustCompile(
    `^https://www\.alltrails\.com/(?:[a-z]{2}-[a-z]{2}/)?trail/[a-z0-9-]+/[a-z0-9-]+/[a-z0-9-]+`,
)

func ValidateURL(rawURL string) error
```

Returns a typed error on failure so the handler can distinguish validation
errors from upstream errors.

### 4.2 Trail ID Extraction (`extract.go`)

AllTrails pages are React-rendered and embed initial server state as a JSON
blob inside a `<script>` tag. The numeric trail ID is present in that blob.

**Extraction strategy:**

1. Parse the HTML with `golang.org/x/net/html`.
2. Walk `<script>` nodes to find one whose text content contains the trail slug
   (derived from the URL).
3. Within that script content, apply a regex to extract the numeric ID:
   ```
   "id"\s*:\s*(\d{6,12})
   ```
4. Return the first match that appears in proximity to the trail slug.

```go
// ExtractTrailID parses raw HTML and returns the numeric AllTrails trail ID.
func ExtractTrailID(html []byte, trailSlug string) (string, error)
```

> **Implementation note:** The exact JSON key names and structure must be
> confirmed against a live AllTrails page at implementation time. The function
> is isolated in `extract.go` with its own tests using fixture HTML, so it can
> be updated independently if AllTrails changes its page structure.

### 4.3 HTTP Client (`client.go`)

```go
type Client struct {
    http      *http.Client
    userAgent string
}

func NewClient(timeout time.Duration) *Client

// FetchTrailJSON orchestrates the two-step fetch:
//   1. GET trail page HTML → ExtractTrailID
//   2. GET AllTrails API → trail JSON bytes
func (c *Client) FetchTrailJSON(ctx context.Context, trailURL string) ([]byte, error)
```

**Browser-mimicking headers** sent on all requests:

| Header | Value |
|---|---|
| `User-Agent` | A current desktop Chrome UA string (configurable at compile time) |
| `Accept` | `text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8` (page) / `application/json` (API) |
| `Referer` | The original trail page URL (for the API call) |
| `Accept-Language` | `en-US,en;q=0.9` |

**AllTrails API URL:**
```
GET https://www.alltrails.com/api/alltrails/v3/trails/{id}?detail=offline
```

**Error mapping:**

| HTTP Status | Returned Error |
|---|---|
| `404` | `ErrTrailNotFound` |
| `429` | `ErrRateLimited` |
| `5xx` | `ErrUpstreamUnavailable` |
| Network timeout | `ErrRequestTimeout` |

All error types are defined in `client.go` and checked by the handler using
`errors.Is` to produce user-friendly messages.

---

## 5. Converter Package (`internal/converter`)

```go
type Converter struct {
    binPath string
    timeout time.Duration
}

func NewConverter(binPath string, timeout time.Duration) (*Converter, error)
// NewConverter verifies binPath is executable on startup; returns error if not found.

// Convert pipes trailJSON into alltrailsgpx and returns the GPX bytes.
func (c *Converter) Convert(ctx context.Context, trailJSON []byte) ([]byte, error)
```

**Subprocess execution:**

```go
cmd := exec.CommandContext(ctx, c.binPath)
cmd.Stdin  = bytes.NewReader(trailJSON)
var stdout, stderr bytes.Buffer
cmd.Stdout = &stdout
cmd.Stderr = &stderr
err := cmd.Run()
```

- `exec.CommandContext` ties the subprocess lifetime to the request context.
  If the context is cancelled (client disconnect or timeout), the process
  receives `SIGKILL`.
- `stderr` is captured and logged at `DEBUG` level; it is never sent to the
  client.
- A non-zero exit code returns `ErrConversionFailed`.

**stdout size guard:** A `io.LimitReader` wraps stdout to cap GPX output at
10 MB, preventing memory exhaustion from a malformed or adversarial binary
response.

---

## 6. Cache Package (`internal/cache`)

```go
type Entry struct {
    GPX       []byte
    TrailName string
    ExpiresAt time.Time
}

type Cache struct {
    mu      sync.Mutex
    entries map[string]Entry
    ttl     time.Duration
}

func New(ttl time.Duration) *Cache

// Put stores GPX bytes and returns a URL-safe token.
func (c *Cache) Put(gpx []byte, trailName string) string

// Get retrieves an entry. Returns false if missing or expired.
func (c *Cache) Get(token string) (Entry, bool)

// StartSweep launches a background goroutine that removes expired entries.
// It runs until ctx is cancelled (pass the server's base context).
func (c *Cache) StartSweep(ctx context.Context, interval time.Duration)
```

**Token generation:**
```go
func newToken() string {
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil {
        panic(err) // crypto/rand failure is unrecoverable
    }
    return hex.EncodeToString(b) // 32-char hex string
}
```

Tokens are hex-encoded random bytes — URL-safe without encoding, not guessable.

---

## 7. Handler Package (`internal/handler`)

```go
type Handler struct {
    alltrails *alltrails.Client
    converter *converter.Converter
    cache     *cache.Cache
    templates *template.Template
    log       *slog.Logger
}

func New(
    at *alltrails.Client,
    conv *converter.Converter,
    c *cache.Cache,
    log *slog.Logger,
) (*Handler, error)
// New parses and caches all templates; returns error if any template fails.

func (h *Handler) RegisterRoutes(mux *http.ServeMux)
```

### 7.1 Route Registration

```go
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /",            h.index)
    mux.HandleFunc("POST /convert",    h.convert)
    mux.HandleFunc("GET /gpx/{token}", h.download)
    mux.HandleFunc("GET /health",      h.health)
}
```

### 7.2 `POST /convert`

```go
func (h *Handler) convert(w http.ResponseWriter, r *http.Request) {
    rawURL := r.FormValue("url")

    // 1. Validate
    if err := alltrails.ValidateURL(rawURL); err != nil {
        h.renderError(w, "Please enter a valid AllTrails trail URL.")
        return
    }

    // 2. Fetch JSON
    trailJSON, err := h.alltrails.FetchTrailJSON(r.Context(), rawURL)
    if err != nil {
        h.renderError(w, userMessageFor(err))
        return
    }

    // 3. Convert
    gpxBytes, err := h.converter.Convert(r.Context(), trailJSON)
    if err != nil {
        h.renderError(w, userMessageFor(err))
        return
    }

    // 4. Cache + respond
    trailName := extractTrailNameFromJSON(trailJSON) // best-effort
    token := h.cache.Put(gpxBytes, trailName)
    h.renderSuccess(w, token)
}
```

`userMessageFor` maps typed errors to strings; unrecognised errors fall back to
a generic "unexpected error" message and are logged at `ERROR` level with full
detail.

### 7.3 `GET /gpx/{token}`

```go
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
    token := r.PathValue("token")
    entry, ok := h.cache.Get(token)
    if !ok {
        http.Error(w, "download link not found or expired", http.StatusNotFound)
        return
    }
    filename := safeFilename(entry.TrailName) + ".gpx"
    w.Header().Set("Content-Type", "application/gpx+xml")
    w.Header().Set("Content-Disposition",
        fmt.Sprintf(`attachment; filename="%s"`, filename))
    w.Header().Set("Content-Length", strconv.Itoa(len(entry.GPX)))
    w.Write(entry.GPX)
}
```

`safeFilename` strips non-alphanumeric characters (except spaces and hyphens)
and replaces spaces with underscores.

---

## 8. Templates

Templates are embedded into the binary at compile time:

```go
//go:embed templates/*.html
var templateFS embed.FS
```

Parsed once in `handler.New` using `template.ParseFS`.

### `templates/layout.html`

Full page returned by `GET /`. Contains:
- Tailwind CSS (loaded from CDN)
- HTMX (loaded from CDN)
- The form with `hx-post="/convert"`, `hx-target="#status"`, `hx-indicator="#spinner"`
- An empty `<div id="status">` where HTMX swaps responses

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>AllTrails → GPX</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script src="https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js"></script>
</head>
<body class="min-h-screen bg-gray-50 flex items-center justify-center">
  <div class="w-full max-w-lg p-8 bg-white rounded-2xl shadow">
    <h1 class="text-2xl font-bold mb-6">AllTrails → GPX</h1>
    <form hx-post="/convert" hx-target="#status" hx-indicator="#spinner">
      <label class="block text-sm font-medium mb-1">AllTrails URL</label>
      <input
        type="url" name="url" required
        placeholder="https://www.alltrails.com/trail/..."
        class="w-full border rounded-lg px-3 py-2 mb-4 focus:outline-none focus:ring-2"
      >
      <button
        type="submit"
        class="w-full bg-green-600 text-white rounded-lg py-2 font-semibold hover:bg-green-700"
      >
        Download GPX
        <span id="spinner" class="htmx-indicator ml-2 animate-spin">⟳</span>
      </button>
    </form>
    <div id="status" class="mt-4"></div>
  </div>
</body>
</html>
```

### `templates/fragments.html`

HTMX partial responses. Two named templates:

**`{{template "success" .}}`** — `.Token string`, `.TrailName string`

Renders a success message and an auto-downloading anchor:
```html
{{define "success"}}
<div class="text-green-700 flex items-center gap-2">
  <span>✓ Done! Downloading <strong>{{.TrailName}}</strong>...</span>
  <a id="dl" href="/gpx/{{.Token}}" download class="hidden"></a>
</div>
<script>document.getElementById('dl').click();</script>
{{end}}
```

**`{{template "error" .}}`** — `.Message string`

```html
{{define "error"}}
<div class="text-red-700 flex items-center gap-2">
  <span>✗ {{.Message}}</span>
</div>
{{end}}
```

> The inline `<script>` that triggers the download is the only JavaScript
> written by the app (beyond loading HTMX/Tailwind from CDN). It is safe
> because `Token` is a hex string generated internally — never from user input.

---

## 9. `cmd/server/main.go`

Wire-up and startup sequence:

```go
func main() {
    cfg := config.Load()

    // Logger
    log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: cfg.LogLevel,
    }))

    // Deps
    atClient  := alltrails.NewClient(cfg.HTTPRequestTimeout)
    conv, err := converter.NewConverter(cfg.AlltrailsgpxBin, cfg.ConversionTimeout)
    if err != nil {
        log.Error("alltrailsgpx binary not found", "error", err)
        os.Exit(1)
    }
    gpxCache  := cache.New(cfg.CacheTTL)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    gpxCache.StartSweep(ctx, time.Minute)

    h, err := handler.New(atClient, conv, gpxCache, log)
    if err != nil {
        log.Error("failed to parse templates", "error", err)
        os.Exit(1)
    }

    mux := http.NewServeMux()
    h.RegisterRoutes(mux)

    srv := &http.Server{
        Addr:         ":" + cfg.Port,
        Handler:      mux,
        ReadTimeout:  10 * time.Second,
        WriteTimeout: cfg.HTTPRequestTimeout + cfg.ConversionTimeout + 5*time.Second,
        IdleTimeout:  120 * time.Second,
    }

    log.Info("server starting", "port", cfg.Port)
    go func() {
        if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
            log.Error("server error", "error", err)
            os.Exit(1)
        }
    }()

    <-ctx.Done()
    log.Info("shutting down")
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    srv.Shutdown(shutdownCtx)
}
```

---

## 10. Unit Tests

### `internal/alltrails`

| Test | Approach |
|---|---|
| `TestValidateURL` | Table-driven: valid/invalid URLs, locale prefixes, edge cases |
| `TestExtractTrailID` | Fixture HTML files in `testdata/`; test success + missing ID |
| `TestFetchTrailJSON` | `httptest.Server` simulating AllTrails page + API responses |

### `internal/converter`

| Test | Approach |
|---|---|
| `TestConvert_Success` | Provide valid AllTrails fixture JSON; verify GPX output starts with `<?xml` |
| `TestConvert_InvalidJSON` | Provide garbage JSON; verify `ErrConversionFailed` |
| `TestConvert_Timeout` | Use a context with a 1ms deadline; verify process is killed cleanly |

> Converter tests require `alltrailsgpx` to be present on `$PATH`. They are
> skipped with `t.Skip` if the binary is not found, so CI without the Rust
> toolchain still passes other tests.

### `internal/cache`

| Test | Approach |
|---|---|
| `TestPut_Get` | Store and retrieve; verify bytes and name match |
| `TestGet_Expired` | Store with 1ms TTL; sleep 5ms; verify miss |
| `TestSweep` | Store entry, run sweep, verify map is empty; check no goroutine leak |
| `TestConcurrent` | Parallel puts and gets with `-race` flag |

### `internal/handler`

| Test | Approach |
|---|---|
| `TestConvert_ValidationError` | Post invalid URL; verify error fragment in response body |
| `TestConvert_UpstreamError` | Inject mock client returning `ErrTrailNotFound`; verify error fragment |
| `TestConvert_Success` | Inject mock client + converter; verify success fragment + token |
| `TestDownload_NotFound` | GET `/gpx/badtoken`; verify 404 |
| `TestDownload_Success` | Pre-populate cache; GET `/gpx/{token}`; verify GPX bytes and headers |
| `TestHealth` | GET `/health`; verify 200 |

---

## 11. Dockerfile

```dockerfile
# ── Stage 1: Build Rust binary ──────────────────────────────────────────────
FROM rust:1-slim AS rust-builder
RUN cargo install alltrailsgpx

# ── Stage 2: Build Go binary ─────────────────────────────────────────────────
FROM golang:1.22-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /server ./cmd/server

# ── Stage 3: Runtime ─────────────────────────────────────────────────────────
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=rust-builder /usr/local/cargo/bin/alltrailsgpx /usr/local/bin/alltrailsgpx
COPY --from=go-builder /server /server
EXPOSE 8080
USER nobody:nogroup
ENTRYPOINT ["/server"]
```

---

## 12. Kubernetes Manifests (`k8s/`)

### `deployment.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: alltrails-to-gpx
spec:
  replicas: 1
  selector:
    matchLabels:
      app: alltrails-to-gpx
  template:
    metadata:
      labels:
        app: alltrails-to-gpx
    spec:
      containers:
        - name: alltrails-to-gpx
          image: alltrails-to-gpx:latest
          ports:
            - containerPort: 8080
          env:
            - name: PORT
              value: "8080"
            - name: LOG_LEVEL
              value: "info"
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            requests:
              cpu: "100m"
              memory: "64Mi"
            limits:
              cpu: "500m"
              memory: "256Mi"
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            capabilities:
              drop: ["ALL"]
```

### `service.yaml`

```yaml
apiVersion: v1
kind: Service
metadata:
  name: alltrails-to-gpx
spec:
  selector:
    app: alltrails-to-gpx
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
  type: ClusterIP
```

---

## 13. `go.mod`

```
module github.com/joooostb/alltrails-to-gpx

go 1.22

require golang.org/x/net v0.26.0
```

> Update the module path to match the actual repository URL before first commit.
