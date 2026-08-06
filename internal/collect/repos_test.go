package collect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type treeEnt struct{ path, typ string }

// treeBody marshals a Git Trees API response body from entries + truncated flag.
func treeBody(t *testing.T, ents []treeEnt, truncated bool) string {
	t.Helper()
	type e struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	payload := struct {
		Tree      []e  `json:"tree"`
		Truncated bool `json:"truncated"`
	}{Truncated: truncated}
	for _, x := range ents {
		payload.Tree = append(payload.Tree, e{Path: x.path, Type: x.typ})
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// serveTree serves a fixed status/body for the tree endpoint (the only call
// repoFiles makes) and pins the request shape: a recursive read of the branch's
// tree. status 0 means 200 OK with the given body.
func serveTree(t *testing.T, body string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/acme/web/git/trees/") {
			t.Errorf("request path = %q, want a git/trees ref read", r.URL.Path)
		}
		if got := r.URL.Query().Get("recursive"); got != "1" {
			t.Errorf("recursive = %q, want 1 (a shallow read misses nested paths)", got)
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = io.WriteString(w, body)
	}
}

func blobs(paths ...string) []treeEnt {
	out := make([]treeEnt, len(paths))
	for i, p := range paths {
		out[i] = treeEnt{path: p, typ: "blob"}
	}
	return out
}

func TestRepoFiles(t *testing.T) {
	all := fileFacts{
		codeowners: true, codeownersKnown: true,
		dependabot: true, dependabotKnown: true,
		renovate: true, renovateKnown: true,
	}
	cases := []struct {
		name string
		ents []treeEnt
		want fileFacts
	}{
		{
			name: "all three present",
			ents: blobs(".github/CODEOWNERS", ".github/dependabot.yml", "renovate.json"),
			want: all,
		},
		{
			name: "codeowners in docs, dependabot .yaml, renovate at repo root .renovaterc",
			ents: blobs("docs/CODEOWNERS", ".github/dependabot.yaml", ".renovaterc"),
			want: all,
		},
		{
			name: "renovate under .github only",
			ents: blobs(".github/renovate.json5"),
			want: fileFacts{codeownersKnown: true, dependabotKnown: true, renovate: true, renovateKnown: true},
		},
		{
			name: "none present, complete tree is a definitive absence",
			ents: blobs("README.md", "main.go", ".github/workflows/ci.yml"),
			want: allAbsent(),
		},
		{
			name: "a tree entry (directory) at a config path does not count as the file",
			ents: []treeEnt{{path: ".github/dependabot.yml", typ: "tree"}},
			want: allAbsent(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, serveTree(t, treeBody(t, tc.ents, false), http.StatusOK))
			got := repoFiles(context.Background(), c, "acme", "web", "main")
			if got != tc.want {
				t.Errorf("repoFiles = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A truncated tree proves presence for a matched path but cannot prove absence:
// the hit stays known-true, the misses degrade to unknown (never a false absent).
func TestRepoFilesTruncated(t *testing.T) {
	body := treeBody(t, blobs(".github/dependabot.yml"), true)
	c := testClient(t, serveTree(t, body, http.StatusOK))
	got := repoFiles(context.Background(), c, "acme", "web", "main")
	want := fileFacts{
		codeowners: false, codeownersKnown: false,
		dependabot: true, dependabotKnown: true,
		renovate: false, renovateKnown: false,
	}
	if got != want {
		t.Errorf("repoFiles = %+v, want %+v", got, want)
	}
}

// An empty repository (no default branch, or the tree endpoint's 404/409) is a
// definitive absence: every fact known-false, matching the pre-tree behavior.
func TestRepoFilesEmptyRepo(t *testing.T) {
	cases := []struct {
		name   string
		branch string
		status int
	}{
		{"empty default branch, no call made", "", 0},
		{"409 git repository is empty", "main", http.StatusConflict},
		{"404 no tree", "main", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, serveTree(t, "", tc.status))
			got := repoFiles(context.Background(), c, "acme", "web", tc.branch)
			if got != allAbsent() {
				t.Errorf("repoFiles = %+v, want %+v", got, allAbsent())
			}
		})
	}
}

// A non-404/409 error leaves absence unproven: every fact must be unknown.
func TestRepoFilesUncertain(t *testing.T) {
	c := testClient(t, serveTree(t, "", http.StatusForbidden))
	got := repoFiles(context.Background(), c, "acme", "web", "main")
	if got.codeownersKnown || got.dependabotKnown || got.renovateKnown {
		t.Errorf("all facts must be unknown on 403, got %+v", got)
	}
}

// serveCodeownersErrors serves a fixed status/body for GitHub's CODEOWNERS
// linter endpoint and pins the request path. status 0 means 200 OK with body.
func serveCodeownersErrors(t *testing.T, body string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/web/codeowners/errors" {
			t.Errorf("request path = %q, want the codeowners errors endpoint", r.URL.Path)
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = io.WriteString(w, body)
	}
}

// codeownersPresent is the fileFacts a repo with a proven CODEOWNERS file carries.
var codeownersPresent = fileFacts{codeowners: true, codeownersKnown: true}

func TestCodeownersErrorCount(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "a file GitHub finds no fault with scores zero",
			body: `{"errors":[]}`,
			want: 0,
		},
		{
			name: "each flagged line counts once",
			body: `{"errors":[
				{"line":3,"kind":"Unknown owner","path":".github/CODEOWNERS",
				 "message":"make sure @acme/ghosts exists and has write access"},
				{"line":7,"kind":"Invalid owner","path":".github/CODEOWNERS",
				 "message":"invalid owner"}
			]}`,
			want: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, serveCodeownersErrors(t, tc.body, http.StatusOK))
			got, known := codeownersErrorCount(context.Background(), c, "acme", "web", codeownersPresent)
			if !known {
				t.Fatal("a 200 from the linter is a trustworthy count, got unknown")
			}
			if got != tc.want {
				t.Errorf("codeownersErrorCount = %d, want %d", got, tc.want)
			}
		})
	}
}

// The linter's verdict is unreadable on any non-200. A 404 is included
// deliberately: it means GitHub sees no CODEOWNERS where the tree proved one, so
// the disagreement leaves the count unproven rather than a false clean bill.
func TestCodeownersErrorCountUnreadable(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := testClient(t, serveCodeownersErrors(t, "", status))
			if _, known := codeownersErrorCount(context.Background(), c, "acme", "web", codeownersPresent); known {
				t.Errorf("count must be unknown on %d, got known", status)
			}
		})
	}
}

// Presence drives whether the extra call is worth making at all: a repo proven to
// have no CODEOWNERS has vacuously no errors, and one whose presence is unproven
// cannot be scored. Neither may spend an API call.
func TestCodeownersErrorCountSkipsCallWithoutAProvenFile(t *testing.T) {
	cases := []struct {
		name      string
		files     fileFacts
		wantCount int
		wantKnown bool
	}{
		{
			name:      "no CODEOWNERS is a definitive zero",
			files:     fileFacts{codeowners: false, codeownersKnown: true},
			wantCount: 0,
			wantKnown: true,
		},
		{
			name:      "unproven presence leaves the count unknown",
			files:     fileFacts{codeowners: false, codeownersKnown: false},
			wantCount: 0,
			wantKnown: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected API call to %q; the linter must not be probed here", r.URL.Path)
			})
			got, known := codeownersErrorCount(context.Background(), c, "acme", "web", tc.files)
			if got != tc.wantCount || known != tc.wantKnown {
				t.Errorf("codeownersErrorCount = (%d, %v), want (%d, %v)", got, known, tc.wantCount, tc.wantKnown)
			}
		})
	}
}

// protectionRoutes drives the three endpoints protection() reads. A zero status
// means 200 with the paired body; any other status is returned bare.
type protectionRoutes struct {
	protStatus   int
	protBody     string
	listStatus   int
	listBody     string
	detailStatus int
	detailBody   string
}

func serveProtection(t *testing.T, r protectionRoutes) http.HandlerFunc {
	t.Helper()
	write := func(w http.ResponseWriter, status int, body string) {
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, body)
	}
	return func(w http.ResponseWriter, req *http.Request) {
		p := req.URL.Path
		switch {
		case p == "/repos/acme/web/branches/main/protection":
			write(w, r.protStatus, r.protBody)
		case p == "/repos/acme/web/rulesets":
			write(w, r.listStatus, r.listBody)
		case strings.HasPrefix(p, "/repos/acme/web/rulesets/"):
			write(w, r.detailStatus, r.detailBody)
		default:
			t.Errorf("unexpected request path %q", p)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// unprotected is the body GitHub pairs with its 404 for a branch that genuinely
// carries no classic protection, as opposed to one the token may not read.
const unprotected = `{"message":"Branch not protected"}`

// A token that cannot read branch protection must leave the facts unproven. The
// permissive zero value ("nothing is required") is indistinguishable from a real
// wide-open repo, so emitting it turns a permission gap into a wall of confident
// false failures across every protection rule at once.
func TestProtectionUnreadable(t *testing.T) {
	const noRulesets = `[]`
	const oneRuleset = `[{"id":1,"target":"branch","enforcement":"active"}]`

	cases := map[string]protectionRoutes{
		"classic protection forbidden": {
			protStatus: http.StatusForbidden, listBody: noRulesets,
		},
		"classic protection errors": {
			protStatus: http.StatusInternalServerError, listBody: noRulesets,
		},
		// The bug this guards: a token without admin on the repo gets a 404 whose
		// only difference from an unprotected branch is the message. Reading it as
		// "unprotected" reports every protection rule as a confident failure on a
		// repo that may be fully protected.
		"classic protection 404 for lack of admin": {
			protStatus: http.StatusNotFound, protBody: `{"message":"Not Found"}`,
			listBody: noRulesets,
		},
		// A repo with no rulesets answers 200 with an empty list, so any error
		// here is a read failure rather than an absence.
		"ruleset list forbidden": {
			protStatus: http.StatusNotFound, protBody: unprotected,
			listStatus: http.StatusForbidden,
		},
		"ruleset list errors": {
			protStatus: http.StatusNotFound, protBody: unprotected,
			listStatus: http.StatusBadGateway,
		},
		// Skipping an unreadable ruleset would under-count and could miss the very
		// rule that makes the branch protected.
		"ruleset detail unreadable": {
			protStatus: http.StatusNotFound, protBody: unprotected,
			listBody: oneRuleset, detailStatus: http.StatusInternalServerError,
		},
	}
	for name, routes := range cases {
		t.Run(name, func(t *testing.T) {
			c := testClient(t, serveProtection(t, routes))
			if _, _, ok := protection(context.Background(), c, "acme", "web", "main"); ok {
				t.Error("protection must be unproven when a read fails, got known")
			}
		})
	}
}

// The one error that is an answer: GitHub returns 404 for a branch that simply
// has no classic protection. Paired with an empty ruleset list that proves the
// branch is unprotected, which must stay a scorable fail rather than unknown.
func TestProtectionUnprotectedIsKnown(t *testing.T) {
	c := testClient(t, serveProtection(t, protectionRoutes{
		protStatus: http.StatusNotFound,
		protBody:   unprotected,
		listBody:   `[]`,
	}))
	p, count, ok := protection(context.Background(), c, "acme", "web", "main")
	if !ok {
		t.Fatal("an unprotected branch is proven, not unknown")
	}
	if count != 0 {
		t.Errorf("ruleset_count = %d, want 0", count)
	}
	if p["required_pull_request_reviews"] != false || p["require_code_owner_reviews"] != false {
		t.Errorf("unprotected branch should score all-false, got %+v", p)
	}
}

// An empty repository has no default branch, so there is nothing to protect —
// a definitive answer that must not degrade to unknown.
func TestProtectionEmptyRepoIsKnown(t *testing.T) {
	c := testClient(t, serveProtection(t, protectionRoutes{}))
	if _, _, ok := protection(context.Background(), c, "acme", "web", ""); !ok {
		t.Error("a repo with no default branch is proven unprotected, got unknown")
	}
}

// The readable path still folds classic protection and rulesets together, with
// the stricter value winning per field.
func TestProtectionFoldsStricter(t *testing.T) {
	c := testClient(t, serveProtection(t, protectionRoutes{
		protBody: `{"required_pull_request_reviews":
			{"required_approving_review_count":1,"require_code_owner_reviews":false}}`,
		listBody: `[{"id":1,"target":"branch","enforcement":"active"}]`,
		detailBody: `{"conditions":{"ref_name":{"include":["~DEFAULT_BRANCH"]}},
			"rules":[{"type":"pull_request","parameters":
				{"required_approving_review_count":2,"require_code_owner_review":true}}],
			"bypass_actors":[{"actor_type":"Integration"}]}`,
	}))
	p, count, ok := protection(context.Background(), c, "acme", "web", "main")
	if !ok {
		t.Fatal("both reads succeeded, want known")
	}
	if count != 1 {
		t.Errorf("ruleset_count = %d, want 1", count)
	}
	if p["required_approving_review_count"] != 2 {
		t.Errorf("review count = %v, want 2 (the ruleset is stricter)", p["required_approving_review_count"])
	}
	if p["require_code_owner_reviews"] != true {
		t.Errorf("require_code_owner_reviews = %v, want true", p["require_code_owner_reviews"])
	}
}
