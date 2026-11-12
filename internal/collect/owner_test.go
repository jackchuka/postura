package collect

import (
	"context"
	"net/http"
	"testing"
)

func TestResolveOwner(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/acme":
			_, _ = w.Write([]byte(`{"login":"acme","type":"Organization"}`))
		case "/users/alice":
			_, _ = w.Write([]byte(`{"login":"alice","type":"User"}`))
		case "/users/bob":
			_, _ = w.Write([]byte(`{"login":"bob","type":"User"}`))
		case "/user":
			_, _ = w.Write([]byte(`{"login":"Alice","type":"User"}`))
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	for login, want := range map[string]Owner{
		"acme":  {Login: "acme"},
		"alice": {Login: "alice", User: true, Self: true},
		"bob":   {Login: "bob", User: true},
	} {
		got, err := ResolveOwner(ctx, c, login)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("ResolveOwner(%s) = %+v, want %+v", login, got, want)
		}
	}
	if _, err := ResolveOwner(ctx, c, "ghost"); err == nil {
		t.Error("ResolveOwner(ghost) succeeded, want error")
	}
}

func TestUserReposSkipsForksAndOtherOwners(t *testing.T) {
	repos := `[{"name":"tool","owner":{"login":"alice"}},{"name":"fork","fork":true,"owner":{"login":"alice"}},
		{"name":"shared","owner":{"login":"acme"}}]`
	var paths []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = w.Write([]byte(repos))
	})
	ctx := context.Background()

	got, err := userRepos(ctx, c, Owner{Login: "alice", User: true, Self: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].GetName() != "tool" {
		t.Fatalf("self repos = %v, want [tool]", got)
	}
	if _, err := userRepos(ctx, c, Owner{Login: "alice", User: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/user/repos?affiliation=owner&per_page=100", "/users/alice/repos?per_page=100&type=owner"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("requested %v, want %v", paths, want)
	}
}
