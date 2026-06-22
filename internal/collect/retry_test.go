package collect

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRT returns the queued responses in order, recording how many calls it saw.
type fakeRT struct {
	responses []*http.Response
	calls     int
}

func (f *fakeRT) RoundTrip(*http.Request) (*http.Response, error) {
	r := f.responses[f.calls]
	f.calls++
	return r, nil
}

func resp(status int, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(""))}
}

func newTestTransport(base http.RoundTripper) *retryTransport {
	t := newRetryTransport(base)
	t.after = func(time.Duration) <-chan time.Time { // don't actually sleep
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	return t
}

func do(t *testing.T, rt *retryTransport) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.github.com/x", nil)
	r, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	return r
}

func TestRetryOnSecondaryLimitThenSuccess(t *testing.T) {
	base := &fakeRT{responses: []*http.Response{
		resp(http.StatusTooManyRequests, map[string]string{"Retry-After": "1"}),
		resp(http.StatusOK, nil),
	}}
	r := do(t, newTestTransport(base))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", r.StatusCode)
	}
	if base.calls != 2 {
		t.Fatalf("calls: got %d, want 2", base.calls)
	}
}

func TestRetryOnPrimaryLimitUsesReset(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(2*time.Second).Unix(), 10)
	base := &fakeRT{responses: []*http.Response{
		resp(http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset}),
		resp(http.StatusOK, nil),
	}}
	r := do(t, newTestTransport(base))
	if r.StatusCode != http.StatusOK || base.calls != 2 {
		t.Fatalf("got status %d after %d calls, want 200 after 2", r.StatusCode, base.calls)
	}
}

func TestNoRetryOnForbiddenWithQuota(t *testing.T) {
	// A 403 for missing scope (quota not exhausted) must not be retried.
	base := &fakeRT{responses: []*http.Response{
		resp(http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "4999"}),
	}}
	r := do(t, newTestTransport(base))
	if r.StatusCode != http.StatusForbidden || base.calls != 1 {
		t.Fatalf("got status %d after %d calls, want 403 after 1", r.StatusCode, base.calls)
	}
}

func TestRetryGivesUpAfterMaxRetry(t *testing.T) {
	var rs []*http.Response
	for range 10 {
		rs = append(rs, resp(http.StatusServiceUnavailable, nil))
	}
	base := &fakeRT{responses: rs}
	rt := newTestTransport(base)
	r := do(t, rt)
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", r.StatusCode)
	}
	if base.calls != rt.maxRetry+1 { // initial try + maxRetry retries
		t.Fatalf("calls: got %d, want %d", base.calls, rt.maxRetry+1)
	}
}

func TestNoRetryOnOverCapDelay(t *testing.T) {
	// A Retry-After beyond maxWait surfaces the response instead of stalling.
	base := &fakeRT{responses: []*http.Response{
		resp(http.StatusTooManyRequests, map[string]string{"Retry-After": "100000"}),
	}}
	r := do(t, newTestTransport(base))
	if r.StatusCode != http.StatusTooManyRequests || base.calls != 1 {
		t.Fatalf("got status %d after %d calls, want 429 after 1", r.StatusCode, base.calls)
	}
}
