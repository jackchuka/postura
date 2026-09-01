package collect

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
)

// alertJSON renders one org-scoped Dependabot alert: a severity, an age in days,
// and the repo it belongs to. Severity is carried where GitHub puts the alert's
// own rating: security_vulnerability, not the advisory-wide roll-up.
func alertJSON(repo, severity string, ageDays int) string {
	created := time.Now().UTC().AddDate(0, 0, -ageDays).Format(time.RFC3339)
	return fmt.Sprintf(
		`{"number":1,"state":"open","created_at":%q,"security_vulnerability":{"severity":%q},"repository":{"name":%q}}`,
		created, severity, repo)
}

// serveOrgAlerts serves one page of the org Dependabot alerts endpoint and pins
// the request shape: open alerts only, since fixed/dismissed ones are not debt.
func serveOrgAlerts(t *testing.T, alerts []string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orgs/acme/dependabot/alerts" {
			t.Errorf("request path = %q, want the org dependabot alerts endpoint", r.URL.Path)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("state = %q, want open", got)
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = io.WriteString(w, "["+strings.Join(alerts, ",")+"]")
	}
}

func sev(count, oldest int) map[string]any {
	return map[string]any{"count": count, "oldest_open_days": oldest}
}

// zeroAlerts is the fact a repo with no open alerts carries: every bucket at
// zero, so an SLA rule passes without needing a guard for the empty case.
func zeroAlerts() map[string]any {
	return map[string]any{
		"total":    0,
		"critical": sev(0, 0),
		"high":     sev(0, 0),
		"medium":   sev(0, 0),
		"low":      sev(0, 0),
	}
}

func TestDependabotAlertsBucketsBySeverityAndAge(t *testing.T) {
	c := testClient(t, serveOrgAlerts(t, []string{
		alertJSON("web", "critical", 42),
		alertJSON("web", "high", 11),
		alertJSON("web", "high", 3),
		alertJSON("api", "medium", 90),
	}, http.StatusOK))

	got, ok := dependabotAlerts(context.Background(), c, "acme")
	if !ok {
		t.Fatal("ok = false, want true on a complete sweep")
	}

	web := map[string]any{
		"total":    3,
		"critical": sev(1, 42),
		"high":     sev(2, 11), // oldest of the two, not the newest
		"medium":   sev(0, 0),
		"low":      sev(0, 0),
	}
	api := map[string]any{
		"total":    1,
		"critical": sev(0, 0),
		"high":     sev(0, 0),
		"medium":   sev(1, 90),
		"low":      sev(0, 0),
	}
	want := map[string]any{"acme/web": web, "acme/api": api}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependabotAlerts = %#v, want %#v", got, want)
	}
}

// A repo the sweep never mentions has no open alerts — a proven zero, not an
// unknown, since the sweep covers the whole org.
func TestDependabotAlertsZeroForUnmentionedRepo(t *testing.T) {
	c := testClient(t, serveOrgAlerts(t, nil, http.StatusOK))
	got, ok := dependabotAlerts(context.Background(), c, "acme")
	if !ok {
		t.Fatal("ok = false, want true on an empty-but-complete sweep")
	}
	if len(got) != 0 {
		t.Errorf("got %#v, want an empty map (callers zero-fill)", got)
	}
	if !reflect.DeepEqual(alertsFor(got, "acme", "web"), zeroAlerts()) {
		t.Errorf("alertsFor unmentioned repo = %#v, want %#v", alertsFor(got, "acme", "web"), zeroAlerts())
	}
}

// An incomplete sweep must report ok=false so repoFacts omits the fact and the
// rule reports unknown — never a false "no alerts".
func TestDependabotAlertsUnreadable(t *testing.T) {
	c := testClient(t, serveOrgAlerts(t, nil, http.StatusForbidden))
	got, ok := dependabotAlerts(context.Background(), c, "acme")
	if ok {
		t.Errorf("ok = true on a 403 sweep, want false (unknown, not empty)")
	}
	if got != nil {
		t.Errorf("got = %#v, want nil on an unreadable sweep", got)
	}
}

// serveRepoFacts answers every call repoFacts makes. The repo alert listing is
// answered explicitly (repoAlerts is the fallback when no org sweep was handed
// in); anything else returns an empty 200.
func serveRepoFacts(alerts []string, alertStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/acme/web/dependabot/alerts" {
			if alertStatus != 0 && alertStatus != http.StatusOK {
				w.WriteHeader(alertStatus)
				return
			}
			_, _ = io.WriteString(w, "["+strings.Join(alerts, ",")+"]")
			return
		}
		_, _ = io.WriteString(w, "{}")
	}
}

// The fact is zero-filled from a completed sweep, and absent when neither the
// sweep nor the repo listing could be read — the difference between "no open
// alerts" and "cannot tell".
func TestRepoFactsCarriesAlertsOnlyWhenReadable(t *testing.T) {
	repo := &github.Repository{Name: github.Ptr("web")}

	c := testClient(t, serveRepoFacts(nil, http.StatusForbidden))
	got := repoFacts(context.Background(), c, "acme", repo, orgWide{alerts: map[string]any{}, alertsOK: true})
	if !reflect.DeepEqual(got["dependabot_alerts"], zeroAlerts()) {
		t.Errorf("dependabot_alerts = %#v, want %#v", got["dependabot_alerts"], zeroAlerts())
	}

	got = repoFacts(context.Background(), c, "acme", repo, orgWide{})
	if _, ok := got["dependabot_alerts"]; ok {
		t.Errorf("dependabot_alerts present with no readable listing, want absent (unknown)")
	}
}

// A repo known to have Dependabot alerts disabled carries no alert fact on
// either path: the org sweep never mentions it (a zero-fill would be a false
// clean), and its own listing is a guaranteed 403 that must not even be tried.
// The same absence on both paths keeps org-wide and repo-subset runs agreeing.
func TestRepoFactsSkipsAlertsWhenDisabled(t *testing.T) {
	repo := &github.Repository{Name: github.Ptr("web")}
	h := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/web/vulnerability-alerts":
			w.WriteHeader(http.StatusNotFound) // alerts disabled
		case "/repos/acme/web/dependabot/alerts":
			t.Error("repo alert listing requested for an alerts-disabled repo (guaranteed 403)")
			w.WriteHeader(http.StatusForbidden)
		default:
			_, _ = io.WriteString(w, "{}")
		}
	}
	c := testClient(t, h)

	// Org-wide path: a completed sweep must not zero-fill the disabled repo.
	got := repoFacts(context.Background(), c, "acme", repo, orgWide{alerts: map[string]any{}, alertsOK: true})
	if _, ok := got["dependabot_alerts"]; ok {
		t.Errorf("dependabot_alerts = %#v with alerts disabled, want absent", got["dependabot_alerts"])
	}

	// Repo-subset path: the per-repo fallback must be skipped, not attempted.
	got = repoFacts(context.Background(), c, "acme", repo, orgWide{})
	if _, ok := got["dependabot_alerts"]; ok {
		t.Errorf("dependabot_alerts = %#v with alerts disabled, want absent", got["dependabot_alerts"])
	}
}

// With no org sweep — an explicit repo subset, or a token that cannot read the
// org endpoint — the repo's own listing supplies the fact. Without this a
// repo-scoped run reports unknown for every repo it could actually have read.
func TestRepoFactsFallsBackToRepoListing(t *testing.T) {
	repo := &github.Repository{Name: github.Ptr("web")}
	c := testClient(t, serveRepoFacts([]string{alertJSON("web", "critical", 21)}, http.StatusOK))

	got := repoFacts(context.Background(), c, "acme", repo, orgWide{})
	want := map[string]any{
		"total":    1,
		"critical": sev(1, 21),
		"high":     sev(0, 0),
		"medium":   sev(0, 0),
		"low":      sev(0, 0),
	}
	if !reflect.DeepEqual(got["dependabot_alerts"], want) {
		t.Errorf("dependabot_alerts = %#v, want %#v", got["dependabot_alerts"], want)
	}
}

// An alert GitHub reports without a usable created_at has an unknown age, not a
// fresh one: it counts, but leaves oldest_open_days out so an SLA rule reports
// unknown instead of a zero that sits inside every window.
func TestDependabotAlertsOmitsAgeForUndatableAlert(t *testing.T) {
	undated := `{"number":1,"state":"open","security_vulnerability":{"severity":"critical"},"repository":{"name":"web"}}`
	c := testClient(t, serveOrgAlerts(t, []string{undated}, http.StatusOK))

	got, ok := dependabotAlerts(context.Background(), c, "acme")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	crit := got["acme/web"].(map[string]any)["critical"].(map[string]any)
	if crit["count"] != 1 {
		t.Errorf("count = %v, want 1 (the alert still counts)", crit["count"])
	}
	if _, ok := crit["oldest_open_days"]; ok {
		t.Errorf("oldest_open_days = %v, want absent (undatable is unknown age, not age 0)", crit["oldest_open_days"])
	}

	// A datable alert alongside an undatable one establishes only a lower
	// bound, not a worst case: the bucket still omits oldest_open_days so the
	// undatable alert cannot hide behind a fresher, datable neighbor.
	c = testClient(t, serveOrgAlerts(t, []string{undated, alertJSON("web", "critical", 30)}, http.StatusOK))
	got, _ = dependabotAlerts(context.Background(), c, "acme")
	crit = got["acme/web"].(map[string]any)["critical"].(map[string]any)
	if crit["count"] != 2 {
		t.Errorf("count = %v, want 2", crit["count"])
	}
	if _, ok := crit["oldest_open_days"]; ok {
		t.Errorf("oldest_open_days = %v, want absent (a mixed bucket's datable max is only a lower bound)", crit["oldest_open_days"])
	}
}

// The alert's own rating lives in security_vulnerability; the advisory-wide
// severity is the max across every package the GHSA covers and can differ.
// Bucketing by the roll-up would score alerts against the wrong SLA window.
func TestDependabotAlertsBucketsByVulnerabilitySeverity(t *testing.T) {
	created := time.Now().UTC().AddDate(0, 0, -3).Format(time.RFC3339)
	mismatched := fmt.Sprintf(
		`{"number":1,"state":"open","created_at":%q,"security_advisory":{"severity":"high"},"security_vulnerability":{"severity":"critical"},"repository":{"name":"web"}}`,
		created)
	// An alert missing the per-package rating falls back to the advisory's.
	advisoryOnly := fmt.Sprintf(
		`{"number":2,"state":"open","created_at":%q,"security_advisory":{"severity":"low"},"repository":{"name":"web"}}`,
		created)
	c := testClient(t, serveOrgAlerts(t, []string{mismatched, advisoryOnly}, http.StatusOK))

	got, ok := dependabotAlerts(context.Background(), c, "acme")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	web := got["acme/web"].(map[string]any)
	if !reflect.DeepEqual(web["critical"], sev(1, 3)) {
		t.Errorf("critical = %#v, want %#v (security_vulnerability.severity, not the advisory roll-up)", web["critical"], sev(1, 3))
	}
	if !reflect.DeepEqual(web["high"], sev(0, 0)) {
		t.Errorf("high = %#v, want %#v (the advisory-wide severity must not bucket the alert)", web["high"], sev(0, 0))
	}
	if !reflect.DeepEqual(web["low"], sev(1, 3)) {
		t.Errorf("low = %#v, want %#v (advisory severity is the fallback)", web["low"], sev(1, 3))
	}
}

// A cursor that does not advance must end the walk — following it would
// re-request the same page forever, with no page cap and no deadline of its
// own — and the walk must report unreadable: the page the stuck cursor served
// was a duplicate, so the tally is truncated and double-counted, never a
// complete listing.
func TestDependabotAlertsStopsOnNonAdvancingCursor(t *testing.T) {
	pages := 0
	h := func(w http.ResponseWriter, r *http.Request) {
		pages++
		// Stop advertising a next page once the walk should long have given up,
		// so a regressed guard shows up as the page-count check failing below
		// rather than an endless loop (t.Fatalf must not run on this goroutine).
		if pages <= 5 {
			w.Header().Set("Link", `<`+r.URL.Path+`?after=stuck>; rel="next"`)
		}
		_, _ = io.WriteString(w, "["+alertJSON("web", "low", 1)+"]")
	}

	got, ok := dependabotAlerts(context.Background(), testClient(t, h), "acme")
	if ok {
		t.Error("ok = true on a non-advancing cursor, want false (truncated, double-counted tally)")
	}
	if got != nil {
		t.Errorf("got = %#v, want nil on a malformed listing", got)
	}
	if pages != 2 {
		t.Errorf("requested %d pages, want 2 (first, then the repeated cursor once)", pages)
	}
}

// The sweep must follow the endpoint's cursor pagination. Stopping after the
// first page would silently under-report alerts on any org with more than a
// page of them — a false pass, not a visible error.
func TestDependabotAlertsFollowsCursorPages(t *testing.T) {
	var seen []string
	h := func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("after")
		seen = append(seen, after)
		switch after {
		case "":
			w.Header().Set("Link", `<`+r.URL.Path+`?after=cursor2>; rel="next"`)
			_, _ = io.WriteString(w, "["+alertJSON("web", "critical", 5)+"]")
		case "cursor2":
			_, _ = io.WriteString(w, "["+alertJSON("web", "critical", 40)+"]")
		default:
			t.Errorf("unexpected cursor %q", after)
		}
	}

	got, ok := dependabotAlerts(context.Background(), testClient(t, h), "acme")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if len(seen) != 2 {
		t.Fatalf("requested %d pages (cursors %v), want 2", len(seen), seen)
	}
	web := got["acme/web"].(map[string]any)
	if !reflect.DeepEqual(web["critical"], sev(2, 40)) {
		t.Errorf("critical = %#v, want %#v (both pages folded, oldest kept)", web["critical"], sev(2, 40))
	}
}
