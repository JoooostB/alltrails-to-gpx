package alltrails

import "errors"

// Typed errors returned by this package so callers can distinguish error
// categories without string matching.
var (
	ErrInvalidURL     = errors.New("invalid AllTrails trail URL")
	ErrTrailNotFound  = errors.New("trail page not found (HTTP 404)")
	ErrIDNotFound     = errors.New("could not extract trail ID from page")
	ErrAPIUnavailable = errors.New("AllTrails API returned an error")
)

// ErrCaptchaChallenge is returned when AllTrails responds with a bot-detection
// challenge (DataDome). The server-side request cannot bypass this; the user
// must fetch the trail JSON manually from their browser.
type ErrCaptchaChallenge struct {
	TrailURL string // original trail page URL the user submitted
}

func (e *ErrCaptchaChallenge) Error() string {
	return "AllTrails returned a bot-detection challenge"
}
