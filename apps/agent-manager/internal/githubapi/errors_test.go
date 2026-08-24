package githubapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestIsNotFoundAndIsUnauthorized(t *testing.T) {
	notFoundClient := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(t, w, map[string]any{"message": "Not Found"})
	})
	_, err := notFoundClient.GetIssue(context.Background(), "o", "r", 1)
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got %v", err)
	}
	if IsUnauthorized(err) {
		t.Fatalf("404 must not be reported as unauthorized")
	}

	forbiddenClient := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		writeJSON(t, w, map[string]any{"message": "Forbidden"})
	})
	_, err = forbiddenClient.GetIssue(context.Background(), "o", "r", 1)
	if !IsUnauthorized(err) {
		t.Fatalf("expected IsUnauthorized, got %v", err)
	}
	if IsNotFound(err) {
		t.Fatalf("403 must not be reported as not found")
	}
}

func TestAsRateLimitPrimary(t *testing.T) {
	reset := time.Now().Add(2 * time.Minute)
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		writeJSON(t, w, map[string]any{"message": "API rate limit exceeded"})
	})

	_, err := client.GetIssue(context.Background(), "o", "r", 1)
	if err == nil {
		t.Fatalf("expected an error")
	}
	limit, ok := AsRateLimit(err)
	if !ok {
		t.Fatalf("expected a rate limit error, got %v", err)
	}
	if limit.RetryAfter <= 0 || limit.RetryAfter > 3*time.Minute {
		t.Fatalf("RetryAfter = %v, want roughly 2m", limit.RetryAfter)
	}
	if IsUnauthorized(err) {
		t.Fatalf("a rate limit error must not be reported as unauthorized")
	}
}

func TestAsRateLimitAbuse(t *testing.T) {
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusForbidden)
		writeJSON(t, w, map[string]any{
			"message":           "You have triggered an abuse detection mechanism",
			"documentation_url": "https://docs.github.com/rest/overview/rate-limits-for-the-rest-api#about-secondary-rate-limits",
		})
	})

	_, err := client.GetIssue(context.Background(), "o", "r", 1)
	limit, ok := AsRateLimit(err)
	if !ok {
		t.Fatalf("expected an abuse rate limit error, got %v", err)
	}
	if limit.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %v, want 30s", limit.RetryAfter)
	}
}
