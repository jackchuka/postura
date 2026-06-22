package collect

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"time"
)

// retryTransport retries requests that hit a GitHub rate limit (primary 403 with
// the quota exhausted, secondary 429) or a transient 5xx, honoring Retry-After /
// X-RateLimit-Reset for the delay. Attempts and total wait are both bounded, and
// it never sleeps past the request's context deadline. The bounded-concurrency
// repo sweep makes secondary-limit hits more likely, so this keeps a large audit
// from failing on a transient throttle rather than a real policy result.
type retryTransport struct {
	base     http.RoundTripper
	maxRetry int
	maxWait  time.Duration
	now      func() time.Time
	after    func(time.Duration) <-chan time.Time // injectable for tests
}

func newRetryTransport(base http.RoundTripper) *retryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryTransport{
		base:     base,
		maxRetry: 4,
		maxWait:  90 * time.Second,
		now:      time.Now,
		after:    time.After,
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the body so it can be replayed on retry (the GraphQL POST has one).
	if req.Body != nil && req.GetBody == nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
		req.Body, _ = req.GetBody()
	}

	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil || attempt >= t.maxRetry || !retryable(resp) {
			return resp, err
		}
		wait := t.backoff(resp, attempt)
		if wait <= 0 || wait > t.maxWait {
			return resp, nil // unknown or over-cap delay: surface the response, don't stall
		}
		drainResp(resp)
		if req.GetBody != nil {
			body, berr := req.GetBody()
			if berr != nil {
				return resp, nil
			}
			req.Body = body
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-t.after(wait):
		}
	}
}

// retryable reports whether a response should be retried: a secondary limit
// (429), a transient gateway error (502/503/504), or a primary rate limit (403
// only when the remaining quota is zero — a 403 for missing scope must not loop).
func retryable(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case http.StatusForbidden:
		return resp.Header.Get("X-RateLimit-Remaining") == "0"
	}
	return false
}

// backoff is the delay before the next attempt: Retry-After if present, else the
// time until X-RateLimit-Reset, else exponential (1s, 2s, 4s, 8s).
func (t *retryTransport) backoff(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if unix, err := strconv.ParseInt(reset, 10, 64); err == nil {
			if d := time.Unix(unix, 0).Sub(t.now()); d > 0 {
				return d + time.Second // small cushion past the reset boundary
			}
		}
	}
	return time.Duration(1<<attempt) * time.Second
}

func drainResp(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}
