package ui_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/testutil"
	"github.com/Waiivyy/turnback/internal/ui"
)

const addr = "127.0.0.1:4812"

// served records two turns and returns a handler for them.
func served(t *testing.T) (http.Handler, string) {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Write("a.txt", "one\n")
	repo.Commit("initial")
	a, err := app.Open(repo.Dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for i, desc := range []string{"Add a line", `Say <script>alert(1)</script>`} {
		if _, err := a.Start(app.StartOptions{Description: desc}); err != nil {
			t.Fatal(err)
		}
		repo.Write("a.txt", strings.Repeat("line\n", i+2))
		if _, err := a.End(app.EndOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := ui.New(a, addr)
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Token()
}

func get(t *testing.T, h http.Handler, path, host, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	if token != "" {
		req.Header.Set("X-Turnback-Token", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEveryRequestNeedsTheToken(t *testing.T) {
	h, token := served(t)
	for _, path := range []string{"/", "/api/turns", "/api/turns/1", "/api/state"} {
		if rec := get(t, h, path, addr, ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s without a token: status %d, want 403", path, rec.Code)
		}
		if rec := get(t, h, path, addr, "wrong"); rec.Code != http.StatusForbidden {
			t.Errorf("%s with a wrong token: status %d, want 403", path, rec.Code)
		}
	}
	if rec := get(t, h, "/?token="+token, addr, ""); rec.Code != http.StatusOK {
		t.Errorf("page with the token in the URL: status %d, want 200", rec.Code)
	}
}

func TestRequestsForAnotherHostAreRefused(t *testing.T) {
	h, token := served(t)
	// A web page that rebinds its own domain to 127.0.0.1 still sends its name.
	if rec := get(t, h, "/api/turns", "evil.example:4812", token); rec.Code != http.StatusForbidden {
		t.Errorf("foreign Host: status %d, want 403", rec.Code)
	}
	if rec := get(t, h, "/api/turns", "localhost:4812", token); rec.Code != http.StatusOK {
		t.Errorf("localhost Host: status %d, want 200", rec.Code)
	}
}

func TestThePageAllowsOnlyItsOwnScript(t *testing.T) {
	h, token := served(t)
	rec := get(t, h, "/?token="+token, addr, "")
	csp := rec.Header().Get("Content-Security-Policy")
	m := regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9+/=]+)'`).FindStringSubmatch(csp)
	if m == nil || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<script nonce="`+m[1]+`">`) {
		t.Error("the page's script does not carry the policy's nonce")
	}
	if strings.Contains(body, "{{nonce}}") {
		t.Error("the nonce placeholder was left in the page")
	}
	for header, want := range map[string]string{
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestTheAPIServesTurnsAndDiffs(t *testing.T) {
	h, token := served(t)
	rec := get(t, h, "/api/turns", addr, token)
	var turns []struct {
		ID          int    `json:"id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &turns); err != nil {
		t.Fatalf("turns are not JSON: %v\n%s", err, rec.Body)
	}
	if len(turns) != 2 || turns[0].ID != 2 || turns[0].Description != `Say <script>alert(1)</script>` {
		t.Errorf("turns = %+v", turns)
	}

	rec = get(t, h, "/api/turns/1", addr, token)
	var turn struct {
		ID   int    `json:"id"`
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &turn); err != nil {
		t.Fatalf("turn is not JSON: %v", err)
	}
	if turn.ID != 1 || !strings.Contains(turn.Diff, "+line") {
		t.Errorf("turn 1 = %+v", turn)
	}
	if rec := get(t, h, "/api/turns/9", addr, token); rec.Code != http.StatusNotFound {
		t.Errorf("unknown turn: status %d, want 404", rec.Code)
	}

	rec = get(t, h, "/api/state", addr, token)
	var state struct {
		Repo      string `json:"repo"`
		Recording *struct {
			Description string `json:"description"`
		} `json:"recording"`
		Hook bool `json:"hook"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil || state.Repo == "" || state.Recording != nil || state.Hook {
		t.Errorf("state = %+v, %v", state, err)
	}
}

func TestOnlyGetRequestsAreServed(t *testing.T) {
	h, token := served(t)
	req := httptest.NewRequest(http.MethodPost, "/api/turns", nil)
	req.Host = addr
	req.Header.Set("X-Turnback-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}
