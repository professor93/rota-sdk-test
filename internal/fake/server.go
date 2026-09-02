package fake

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	rota "github.com/professor93/rota/lib"
)

// Handler answers one path. body is the decoded JSON object when the request
// carried application/json, otherwise the parsed form; that one shape serves
// claude (JSON) and codex (form-encoded) alike. A string reply is written
// raw; anything else is JSON-encoded.
type Handler func(r *http.Request, body map[string]any) (status int, reply any)

// Request is what the server saw, kept for assertions on headers and bodies.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   map[string]any
}

// Server is an httptest server that plays whichever provider endpoints a
// test wires up. An unregistered path answers 404 and is still recorded.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	handlers map[string]Handler
	requests []Request
}

// NewServer starts a server that is closed at cleanup.
func NewServer(t testing.TB) *Server {
	t.Helper()
	s := &Server{handlers: map[string]Handler{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Handle answers path with h.
func (s *Server) Handle(path string, h Handler) {
	s.mu.Lock()
	s.handlers[path] = h
	s.mu.Unlock()
}

// Requests is everything seen so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Hits counts the requests to one path.
func (s *Server) Hits(path string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Path == path {
			n++
		}
	}
	return n
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{}
	raw, _ := io.ReadAll(r.Body)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &body)
	} else if len(raw) > 0 {
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		if err := r.ParseForm(); err == nil {
			for k, v := range r.PostForm {
				if len(v) > 0 {
					body[k] = v[0]
				}
			}
		}
	}
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	h := s.handlers[r.URL.Path]
	s.mu.Unlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	status, reply := h(r, body)
	if str, ok := reply.(string); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, str)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(reply)
}

// Claude points every claude endpoint at this server and installs its
// client: /authorize, /token, /profile and /usage. Restored at cleanup.
func (s *Server) Claude(t testing.TB) {
	t.Helper()
	Restore(t, &rota.HTTPClient, s.Client())
	Restore(t, &rota.ClaudeEndpoints.Authorize, s.URL+"/authorize")
	Restore(t, &rota.ClaudeEndpoints.Token, s.URL+"/token")
	Restore(t, &rota.ClaudeEndpoints.Profile, s.URL+"/profile")
	Restore(t, &rota.ClaudeEndpoints.Usage, s.URL+"/usage")
}

// Codex points both codex endpoints at this server: /authorize and
// /codex/token. Restored at cleanup.
func (s *Server) Codex(t testing.TB) {
	t.Helper()
	Restore(t, &rota.HTTPClient, s.Client())
	Restore(t, &rota.CodexEndpoints.Authorize, s.URL+"/authorize")
	Restore(t, &rota.CodexEndpoints.Token, s.URL+"/codex/token")
}

// Reply is a JSON object literal for handlers.
type Reply = map[string]any

// ClaudeToken is a successful claude token reply. Note the field is
// email_address here and email in the profile reply.
func ClaudeToken(access, refresh string, expiresIn int, uuid, email, org string) Reply {
	r := Reply{"access_token": access, "expires_in": expiresIn, "scope": "user:inference user:profile"}
	if refresh != "" {
		r["refresh_token"] = refresh
	}
	if uuid != "" || email != "" {
		r["account"] = Reply{"uuid": uuid, "email_address": email}
	}
	if org != "" {
		r["organization"] = Reply{"uuid": org}
	}
	return r
}

// ClaudeProfile is the profile reply Identify reads.
func ClaudeProfile(uuid, email, org string) Reply {
	return Reply{"account": Reply{"uuid": uuid, "email": email}, "organization": Reply{"uuid": org}}
}

// ClaudeUsage is the usage reply Quota reads, with a 5-hour and a 7-day
// window at the given utilizations and no extra usage.
func ClaudeUsage(fiveHour, sevenDay float64, resetsAt string) Reply {
	return Reply{
		"five_hour": Reply{"utilization": fiveHour, "resets_at": resetsAt},
		"seven_day": Reply{"utilization": sevenDay, "resets_at": resetsAt},
	}
}

// OAuthReject is the rejection body both providers send: {"error": code}.
func OAuthReject(code, description string) Reply {
	r := Reply{"error": code}
	if description != "" {
		r["error_description"] = description
	}
	return r
}

// JWT builds an unsigned token whose payload is the JSON of claims, the
// shape codex reads sub, email and exp from.
func JWT(claims Reply) string {
	payload, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + ".sig"
}

// CodexToken is a successful codex token reply: an access JWT carrying exp,
// an id_token carrying the identity, and a refresh token.
func CodexToken(refresh, sub, email, accountID string, exp int64) Reply {
	return Reply{
		"access_token":  JWT(Reply{"exp": exp}),
		"refresh_token": refresh,
		"id_token":      JWT(Reply{"sub": sub, "email": email, "https://api.openai.com/auth": Reply{"chatgpt_account_id": accountID}}),
		"expires_in":    864000,
	}
}
