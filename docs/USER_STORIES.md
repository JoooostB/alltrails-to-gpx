# User Stories: AllTrails to GPX

## US-01 — Download a GPX file from an AllTrails URL

**As a** trail runner,
**I want to** paste an AllTrails trail URL and click a button,
**so that** my browser automatically downloads the trail as a GPX file without me having to use developer tools or a command-line tool.

**Acceptance criteria:**
- Given a valid public AllTrails trail URL (e.g. `https://www.alltrails.com/trail/netherlands/north-holland/some-trail`), when I submit the form, then a `.gpx` file download starts in my browser within 30 seconds.
- The downloaded file is named after the trail (e.g. `some-trail.gpx`).
- The GPX file can be imported into Strava, Garmin Connect, or Komoot without errors.

---

## US-02 — See a loading indicator while the conversion is in progress

**As a** trail runner,
**I want to** see a spinner or progress message after I click the button,
**so that** I know the app is working and haven't accidentally clicked twice.

**Acceptance criteria:**
- From the moment the form is submitted until the response is received, a visible loading indicator is shown on or near the button.
- The button is disabled or visually inactive during the request to prevent duplicate submissions.
- The spinner disappears once the response arrives (success or error).

---

## US-03 — See a clear error message when something goes wrong

**As a** trail runner,
**I want to** see a human-readable error message when the conversion fails,
**so that** I understand what went wrong and whether I need to try again or correct the URL.

**Acceptance criteria:**
- If I submit a URL that does not match the AllTrails trail format, I see: *"Please enter a valid AllTrails trail URL (e.g. https://www.alltrails.com/trail/...)"*
- If the trail page cannot be found (e.g. URL points to a deleted trail), I see: *"Trail not found. Please check the URL."*
- If the AllTrails API is unavailable, I see: *"Could not fetch trail data from AllTrails. Please try again later."*
- If the GPX conversion fails, I see: *"GPX conversion failed. The trail data may be in an unsupported format."*
- Error messages never contain raw stack traces or internal error details.

---

## US-04 — Use the app on any device without installing anything

**As a** trail runner who switches between laptop and phone,
**I want to** use the web app from any browser,
**so that** I don't need to install software on every device I use.

**Acceptance criteria:**
- The app is accessible at a URL (homelab-hosted).
- The UI renders correctly on both desktop and mobile screen sizes.
- No JavaScript framework, app install, or browser extension is required.

---

## US-05 — Convert a trail with a localised AllTrails URL

**As a** trail runner in Europe,
**I want to** paste a localised AllTrails URL (e.g. `https://www.alltrails.com/en-gb/trail/...`),
**so that** URLs I copy from the AllTrails website in my locale work without manual editing.

**Acceptance criteria:**
- URLs with a locale prefix (`/en-gb/`, `/de-de/`, etc.) are accepted and processed successfully.
- The resulting GPX file is identical to one produced from the non-localised URL for the same trail.

---

## US-06 — Access a health check endpoint

**As a** homelab operator,
**I want** the app to expose a `/health` endpoint,
**so that** Kubernetes liveness and readiness probes can confirm the app is running.

**Acceptance criteria:**
- `GET /health` returns HTTP `200` with body `ok`.
- The endpoint responds in under 100 ms under normal conditions.
- No conversion or external HTTP request is made when `/health` is called.

---

## US-07 — Download link expires after a short window

**As a** homelab operator concerned about memory usage,
**I want** converted GPX files to be evicted from memory after a short TTL,
**so that** the app does not accumulate unbounded memory over time.

**Acceptance criteria:**
- A generated download token is valid for 5 minutes after creation.
- If a token is accessed after expiry, the response is HTTP `404` with the message: *"Download link expired. Please convert the trail again."*
- Expired entries are swept from memory within 1 minute of their TTL expiring.
