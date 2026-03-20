package alltrails

import (
	"io"
	"regexp"
	"strings"
)

// trailIDPattern matches the numeric trail ID embedded in AllTrails page scripts.
var trailIDPattern = regexp.MustCompile(`"id"\s*:\s*(\d{6,12})`)

// v3TrailsPattern matches trail IDs in AllTrails v3 API URLs found in
// <link rel="preload"> imageSrcSet attributes (e.g. /v3/trails/10928400/static_map).
var v3TrailsPattern = regexp.MustCompile(`/v3/trails/(\d{6,12})/`)

// trailIDFieldPattern matches "trailId":12345678 in RSC streaming payloads.
var trailIDFieldPattern = regexp.MustCompile(`"trailId"\s*:\s*(\d{6,12})`)

// trailURLPattern validates and parses AllTrails trail URLs.
// Anchored at both ends; accepts an optional locale prefix (e.g. /en-gb/).
var trailURLPattern = regexp.MustCompile(
	`^https://www\.alltrails\.com/(?:[a-z]{2}-[a-z]{2}/)?[a-z-]+/([a-z0-9-]+)/([a-z0-9-]+)/([a-z0-9-]+)(?:[?#].*)?$`,
)

// ValidateURL returns ErrInvalidURL if rawURL does not match the expected
// AllTrails trail URL pattern.
func ValidateURL(rawURL string) error {
	if !trailURLPattern.MatchString(rawURL) {
		return ErrInvalidURL
	}
	return nil
}

// SlugFromURL returns the final path segment (trail slug) from a validated URL.
// The caller must have already validated the URL with ValidateURL.
func SlugFromURL(rawURL string) string {
	m := trailURLPattern.FindStringSubmatch(rawURL)
	if len(m) < 4 {
		return ""
	}
	return m[3]
}

// maxPageBody caps the page HTML read to 2 MB to prevent hangs on large SSR pages.
const maxPageBody = 2 * 1024 * 1024

// ExtractTrailID reads the HTML body and returns the numeric trail ID.
//
// AllTrails uses Next.js App Router with React Server Components, so the trail
// ID can appear in several places:
//  1. <link rel="preload"> imageSrcSet URLs: /v3/trails/10928400/static_map
//  2. RSC streaming data: "trailId":10928400
//  3. Legacy <script> tags containing the slug and "id":12345678
//
// The function reads the raw bytes (capped at 2 MB) and tries each pattern.
func ExtractTrailID(body io.Reader, slug string) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxPageBody))
	if err != nil {
		return "", ErrIDNotFound
	}
	page := string(raw)

	// Strategy 1: /v3/trails/{id}/ in <link> preload URLs or anywhere in page.
	if m := v3TrailsPattern.FindStringSubmatch(page); len(m) == 2 {
		return m[1], nil
	}

	// Strategy 2: "trailId":12345678 in RSC streaming payloads.
	if m := trailIDFieldPattern.FindStringSubmatch(page); len(m) == 2 {
		return m[1], nil
	}

	// Strategy 3 (legacy): find a <script> containing the slug, then extract "id".
	if idx := strings.Index(page, slug); idx != -1 {
		// Search around the slug for an "id" field.
		start := idx - 500
		if start < 0 {
			start = 0
		}
		end := idx + 500
		if end > len(page) {
			end = len(page)
		}
		if m := trailIDPattern.FindStringSubmatch(page[start:end]); len(m) == 2 {
			return m[1], nil
		}
	}

	return "", ErrIDNotFound
}
