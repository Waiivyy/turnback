// Package ui serves turnback's local, read-only web page for browsing
// recorded turns.
//
// The page shows code from the working tree, so the server is strict: it
// listens on the loopback interface only, every request must carry a random
// token from the URL printed at start-up, requests naming another host are
// refused (which defeats DNS rebinding), and the page may run only its own
// inline script. It never changes anything; undo stays in the CLI.
package ui

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/store"
)

//go:embed index.html
var page string

// maxDiff is the most diff text sent for one turn. Beyond it the page says
// the diff was cut and points at 'turnback show'.
var maxDiff = 16 << 20

// Server is the web page and its JSON API for one repository.
type Server struct {
	app   *app.App
	addr  string // host:port the page is reached at
	token string
	mux   *http.ServeMux
}

// New returns a server for a, reached at addr (such as 127.0.0.1:4812).
func New(a *app.App, addr string) (*Server, error) {
	token, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	s := &Server{app: a, addr: addr, token: token, mux: http.NewServeMux()}
	s.mux.HandleFunc("/", s.servePage)
	s.mux.HandleFunc("/api/state", s.serveState)
	s.mux.HandleFunc("/api/turns", s.serveTurns)
	s.mux.HandleFunc("/api/turns/", s.serveTurn)
	return s, nil
}

// Token returns the secret every request must carry.
func (s *Server) Token() string { return s.token }

// URL returns the address to open in a browser, token included.
func (s *Server) URL() string { return "http://" + s.addr + "/?token=" + s.token }

// ServeHTTP checks every request before routing it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	switch {
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		http.Error(w, "turnback ui is read-only", http.StatusMethodNotAllowed)
	case !s.hostAllowed(r.Host):
		http.Error(w, "forbidden", http.StatusForbidden)
	case !s.authorized(r):
		http.Error(w, "forbidden: open the URL turnback ui printed, token included", http.StatusForbidden)
	default:
		s.mux.ServeHTTP(w, r)
	}
}

// hostAllowed accepts only loopback names for the server's own port.
func (s *Server) hostAllowed(host string) bool {
	_, port, err := net.SplitHostPort(s.addr)
	if err != nil {
		return false
	}
	for _, name := range []string{"127.0.0.1", "localhost", "[::1]"} {
		if host == name+":"+port {
			return true
		}
	}
	return false
}

func (s *Server) authorized(r *http.Request) bool {
	given := r.Header.Get("X-Turnback-Token")
	if given == "" {
		given = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(s.token)) == 1
}

func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	nonce, err := randomNonce()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'nonce-"+nonce+
		"'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Write([]byte(strings.ReplaceAll(page, "{{nonce}}", nonce)))
}

// state is what the page's header shows. The page polls it and loads the
// turns again only when Turns or Latest change, which is cheap to check.
type state struct {
	Repo      string         `json:"repo"`
	Recording *store.Session `json:"recording"`
	Hook      bool           `json:"hook"`
	Turns     int            `json:"turns"`
	Latest    int            `json:"latest"`
}

func (s *Server) serveState(w http.ResponseWriter, r *http.Request) {
	sess, err := s.app.Store.Session()
	if err != nil {
		serveError(w, err)
		return
	}
	ids, err := s.app.Store.TurnIDs()
	if err != nil {
		serveError(w, err)
		return
	}
	st := state{Repo: filepath.Base(s.app.Root), Recording: sess, Hook: s.app.HookInstalled(), Turns: len(ids)}
	if len(ids) > 0 {
		st.Latest = ids[len(ids)-1]
	}
	serveJSON(w, st)
}

func (s *Server) serveTurns(w http.ResponseWriter, r *http.Request) {
	turns, err := s.app.Store.Turns()
	if err != nil {
		serveError(w, err)
		return
	}
	if turns == nil {
		turns = []*store.Turn{}
	}
	serveJSON(w, turns)
}

func (s *Server) serveTurn(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/turns/"))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	turn, err := s.app.Store.Turn(id)
	if err != nil {
		serveError(w, err)
		return
	}
	diff, err := s.app.Diff(turn, nil)
	if err != nil {
		serveError(w, err)
		return
	}
	truncated := len(diff) > maxDiff
	if truncated {
		diff = diff[:strings.LastIndexByte(diff[:maxDiff], '\n')+1]
	}
	serveJSON(w, struct {
		*store.Turn
		Diff      string `json:"diff"`
		Truncated bool   `json:"truncated,omitempty"`
	}{turn, diff, truncated})
}

func serveJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v) // escapes <, > and & too
}

func serveError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, store.ErrTurnNotFound) {
		code = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomNonce() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
