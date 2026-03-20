# Functional Design: AllTrails to GPX

## 1. Overview

A self-hosted web application that converts a public AllTrails trail URL into a
downloadable GPX file. The user pastes a trail URL; the backend fetches the trail
data from the AllTrails API, converts it using the `alltrailsgpx` binary, and
serves the GPX file as a browser download.

---

## 2. Scope

**In scope:**
- Accept a public AllTrails trail page URL (no user credentials required)
- Fetch trail JSON from the AllTrails API server-side
- Convert trail JSON to GPX using the `alltrailsgpx` binary (subprocess)
- Return the GPX file as a browser download
- Provide loading/success/error feedback via HTMX

**Out of scope (this version):**
- AllTrails authentication
- Direct Strava import (see §10)

---

## 3. System Architecture

```
┌─────────────────────────────────────────────────────┐
│  Browser                                             │
│  ┌──────────────────────────────────────────────┐   │
│  │  HTML + Tailwind CSS + HTMX                  │   │
│  └───────────────────┬──────────────────────────┘   │
└──────────────────────┼──────────────────────────────┘
                       │ HTTP
┌──────────────────────▼──────────────────────────────┐
│  Go HTTP Server                                      │
│                                                      │
│  ┌──────────────────┐   ┌──────────────────────┐    │
│  │  AllTrails HTTP  │   │  In-memory GPX cache │    │
│  │  Client          │   │  (UUID → bytes, TTL) │    │
│  └────────┬─────────┘   └──────────────────────┘    │
│           │ JSON                                     │
│  ┌────────▼─────────┐                               │
│  │  alltrailsgpx    │  (subprocess, stdin/stdout)   │
│  │  binary          │                               │
│  └──────────────────┘                               │
└─────────────────────────────────────────────────────┘
```

**Components:**
1. **Frontend** — server-rendered HTML, Tailwind CSS, HTMX
2. **Go HTTP Server** — handles requests, orchestrates conversion, caches results
3. **AllTrails HTTP Client** — fetches trail page HTML and API JSON (no auth)
4. **`alltrailsgpx` binary** — converts AllTrails JSON to GPX (Rust, subprocess)

---

## 4. User Flow

1. User opens the web app.
2. User pastes a public AllTrails trail URL into the form and clicks **Download GPX**.
3. HTMX submits the form; a loading spinner appears.
4. The backend:
   1. Validates the URL matches the expected AllTrails trail format.
   2. Fetches the AllTrails trail HTML page and extracts the numeric trail ID.
   3. Calls the AllTrails API to retrieve the trail JSON.
   4. Pipes the JSON to `alltrailsgpx` via stdin; reads GPX from stdout.
   5. Stores the GPX bytes in an in-memory cache under a UUID token (5-minute TTL).
   6. Returns an HTML fragment containing a download link.
5. HTMX swaps the fragment into the status area; the browser triggers the download.
6. The user receives the `.gpx` file.

---

## 5. Frontend

**Technology:** Server-rendered HTML · Tailwind CSS · HTMX

### Layout

```
┌──────────────────────────────────────────────┐
│  AllTrails → GPX                             │
│                                              │
│  AllTrails URL                               │
│  [__________________________________________]│
│                                              │
│  [ Download GPX ]   ⟳ Fetching route...     │
│                                              │
│  ✓ Done! Your download has started.         │
└──────────────────────────────────────────────┘
```

### HTMX Behaviour

- The form uses `hx-post="/convert"` and `hx-target="#status"` to swap the
  response into the status area.
- `hx-indicator` shows/hides a spinner on the button while the request is in
  flight.
- **Success response:** an HTML fragment containing a success message and an
  `<a href="/gpx/{token}" download>` link. The link has `hx-trigger="load"`
  plus a small inline script to auto-click it, so the download starts without
  any extra user action.
- **Error response:** an HTML fragment with an error message, styled with a
  warning icon.

### URL Input Validation

Client-side: the input field uses `pattern` and `required` attributes to
enforce that the URL starts with `https://www.alltrails.com/trail/`.
Server-side validation provides the authoritative check.

---

## 6. HTTP API

### `GET /`
Returns the main HTML page.

---

### `POST /convert`

**Request:** `application/x-www-form-urlencoded`

| Field | Description |
|-------|-------------|
| `url` | AllTrails trail page URL |

**Processing (see §7 and §8):**
1. Validate `url` against AllTrails URL pattern.
2. Fetch trail HTML → extract numeric trail ID.
3. Call AllTrails API → trail JSON.
4. Pipe JSON to `alltrailsgpx` → GPX bytes.
5. Store GPX in cache; generate UUID token.
6. Return HTML fragment.

**Responses:**

| Outcome | HTTP Status | Body |
|---------|-------------|------|
| Success | `200 OK` | HTML fragment: success message + download link |
| Validation error | `200 OK` | HTML fragment: inline error message |
| Upstream failure | `200 OK` | HTML fragment: error message |

> All responses return `200` so HTMX performs the swap unconditionally.
> Error state is communicated through the fragment content, not HTTP status.

---

### `GET /gpx/{token}`

Serves the cached GPX file.

| Condition | Response |
|-----------|----------|
| Token valid and not expired | `200` with `Content-Type: application/gpx+xml`, `Content-Disposition: attachment; filename="{trail-name}.gpx"` |
| Token expired or not found | `404` plain text |

---

### `GET /health`

Returns `200 OK` with body `ok`. Used as a Kubernetes liveness/readiness probe.

---

## 7. AllTrails Data Fetching

The `alltrailsgpx` README describes this as a manual step (open DevTools, find
the API request, save the response). The Go backend automates it:

### Step 1 — Extract the numeric trail ID from the URL

AllTrails trail URLs follow the pattern:
```
https://www.alltrails.com/trail/{country}/{state}/{trail-slug}
```

The AllTrails API requires a **numeric trail ID**, not the slug. The backend
fetches the trail HTML page and extracts the ID from the page source (likely
embedded in a JSON-LD `<script type="application/ld+json">` block or an inline
JavaScript object such as `window.__AT_INITIAL_STATE__`).

Extraction uses regex or an HTML parser. If the ID cannot be found, the request
fails with a descriptive error (see §9).

### Step 2 — Call the AllTrails API

```
GET https://www.alltrails.com/api/alltrails/v3/trails/{id}?detail=offline
```

The request is sent with browser-like headers to avoid being rejected:

| Header | Value |
|--------|-------|
| `User-Agent` | A current desktop browser UA string |
| `Referer` | The original trail page URL |
| `Accept` | `application/json` |

The `detail=offline` parameter produces the JSON format that `alltrailsgpx`
expects (`trails[]` root array).

> **Note:** AllTrails may change its page structure or add bot-detection at any
> time. If the ID extraction or API call breaks, the error message will direct
> the user to check that the URL is correct and that the trail is public.

---

## 8. GPX Conversion (alltrailsgpx subprocess)

The `alltrailsgpx` binary converts AllTrails API JSON to GPX format.

### Integration

The Go backend spawns `alltrailsgpx` as a child process:
- AllTrails JSON is written to the process's **stdin**.
- GPX output is read from **stdout**.
- **stderr** is captured for error logging.
- The process's exit code is checked; non-zero indicates a conversion failure.

Conceptual data flow:
```
[AllTrails JSON bytes] → stdin → alltrailsgpx → stdout → [GPX bytes]
```

No intermediate files are written to disk.

### Timeout

The subprocess is given a configurable timeout (default: `15s`). If it exceeds
this, the process is killed and an error is returned to the user.

---

## 9. Error Handling

All errors are returned as HTMX-swappable HTML fragments.

| Error Condition | User-Visible Message |
|-----------------|----------------------|
| URL does not match AllTrails trail pattern | "Please enter a valid AllTrails trail URL (e.g. https://www.alltrails.com/trail/...)" |
| Trail HTML page not found (HTTP 404) | "Trail not found. Please check the URL." |
| Trail ID could not be extracted from page | "Could not read trail data. The page format may have changed." |
| AllTrails API returned an error | "Could not fetch trail data from AllTrails. Please try again later." |
| `alltrailsgpx` exited non-zero | "GPX conversion failed. The trail data may be in an unsupported format." |
| Subprocess timeout | "Conversion timed out. Please try again." |
| Download token not found or expired | "Download link expired. Please convert the trail again." |

All upstream HTTP errors and subprocess errors are logged server-side with full
detail. The user-facing message never includes raw error output.

---

## 10. In-Memory GPX Cache

Converted GPX files are held in a Go `sync.Map` keyed by a UUID token.

| Property | Value |
|----------|-------|
| Key | UUID v4 string |
| Value | `struct { gpxBytes []byte; trailName string; expiresAt time.Time }` |
| TTL | 5 minutes (configurable) |
| Eviction | Background goroutine sweeps expired entries every minute |

The cache is not persisted. A server restart invalidates all tokens (acceptable
for a homelab deployment with a single user).

---

## 11. Configuration (Environment Variables)

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port |
| `ALLTRAILSGPX_BIN` | `alltrailsgpx` | Path to the `alltrailsgpx` binary |
| `HTTP_REQUEST_TIMEOUT` | `30s` | Timeout for AllTrails HTTP requests |
| `CONVERSION_TIMEOUT` | `15s` | Timeout for `alltrailsgpx` subprocess |
| `CACHE_TTL` | `5m` | GPX cache entry TTL |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |

---

## 12. Deployment

### Docker — Multi-Stage Build

```
Stage 1 (rust-builder)   — rust:slim     → builds alltrailsgpx binary
Stage 2 (go-builder)     — golang:alpine → builds Go HTTP server binary
Stage 3 (runtime)        — debian:slim   → copies both binaries; no build tools
```

Exposed port: `8080`.

No volumes or persistent storage required.

### Kubernetes

| Resource | Notes |
|----------|-------|
| `Deployment` | 1 replica; `imagePullPolicy: Always` |
| `Service` | `ClusterIP` (or `NodePort`/`Ingress` if external access is needed) |
| Liveness probe | `GET /health` |
| Readiness probe | `GET /health` |
| Resource requests | TBD after initial profiling |

---

## 13. Future: Strava Integration

A future version could import the GPX directly into Strava without manual
download/upload.

**Proposed flow:**
1. Conversion succeeds → "Import to Strava" button appears alongside the
   download link.
2. User clicks → redirected to Strava's OAuth 2.0 authorization endpoint
   (`scope: activity:write`).
3. After user approval, Strava redirects back to `/oauth/strava/callback` with
   an authorization code.
4. Backend exchanges the code for an access token (not stored; used
   immediately).
5. Backend calls `POST https://www.strava.com/api/v3/uploads` with the GPX
   file (multipart form, `data_type=gpx`).
6. Backend polls `GET /api/v3/uploads/{id}` until the upload is processed.
7. Backend returns a link to the newly created Strava activity.

**Additional requirements:** HTTPS callback URL (required by Strava OAuth),
Strava API credentials (client ID + secret via env vars).
