package githubapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/go-github/v69/github"
)

// RateLimit describes a GitHub rate limit encountered while calling the
// API, including how long the caller should wait before retrying.
type RateLimit struct {
	RetryAfter time.Duration
}

// AsRateLimit reports whether err is a primary or secondary (abuse
// detection) GitHub rate limit error, and if so, how long to wait before
// retrying (ADR-007 section 2: rate limiting is handled inside this
// package, not by callers).
func AsRateLimit(err error) (RateLimit, bool) {
	var rateLimitErr *github.RateLimitError
	if errors.As(err, &rateLimitErr) {
		wait := time.Until(rateLimitErr.Rate.Reset.Time)
		if wait < 0 {
			wait = 0
		}
		return RateLimit{RetryAfter: wait}, true
	}
	var abuseErr *github.AbuseRateLimitError
	if errors.As(err, &abuseErr) {
		wait := time.Minute
		if abuseErr.RetryAfter != nil {
			wait = *abuseErr.RetryAfter
		}
		return RateLimit{RetryAfter: wait}, true
	}
	return RateLimit{}, false
}

// IsNotFound reports whether err is a GitHub 404 response: the resource
// does not exist, or the authenticated identity cannot see it.
func IsNotFound(err error) bool {
	return hasStatusCode(err, http.StatusNotFound)
}

// IsUnauthorized reports whether err is a GitHub 401/403 response that is
// not a rate limit: the stored credential is invalid, expired, or lacks the
// scope required for the request (e.g. Projects access).
func IsUnauthorized(err error) bool {
	if _, ok := AsRateLimit(err); ok {
		return false
	}
	return hasStatusCode(err, http.StatusUnauthorized) || hasStatusCode(err, http.StatusForbidden)
}

func hasStatusCode(err error, code int) bool {
	var errResp *github.ErrorResponse
	if errors.As(err, &errResp) && errResp.Response != nil {
		return errResp.Response.StatusCode == code
	}
	return false
}
