// Package api implements the api-service HTTP handlers, split across
// the two sockets described in DESIGN.md §4.1: a root-only write socket
// (account lookup/creation, presence registration) and a
// world-connectable read socket (listing users, listing who's online, and
// retrieving the configured weather forecast).
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"unixbbs/src/api-service/internal/exam"
	"unixbbs/src/api-service/internal/presence"
	"unixbbs/src/api-service/internal/store"
)

const (
	DefaultWeatherURL       = "https://api.weather.gov/gridpoints/BOX/71,101/forecast"
	DefaultWeatherUserAgent = "unixbbs-api-service"
	maxWeatherResponseSize  = 2 << 20
)

// Provisioner creates the per-uid directories a newly-looked-up user
// needs (see the internal/provision package). It's an interface here,
// rather than a concrete type, so handler tests don't have to satisfy
// provision.Provisioner's real chown requirements.
type Provisioner interface {
	EnsureUserDirs(uid int64) error
}

// recentLoginsLimit is how many rows the `last` command shows.
const recentLoginsLimit = 10

// Handlers holds the shared state used by both sockets' handlers.
type Handlers struct {
	Examiner          *exam.Examiner
	Store             *store.Store
	Presence          *presence.Tracker
	Provisioner       Provisioner
	WeatherURL        string
	HTTPClient        *http.Client
	WeatherUserAgent  string
	WeatherCacheTTL   time.Duration
	weatherCacheMu    sync.Mutex
	weatherCacheBody  []byte
	weatherCacheUntil time.Time
}

// WriteMux returns the handler for api-write.sock: mutating/
// identity-claiming endpoints only. Callers must serve this on a socket
// only root can connect to (mode 0700) -- these handlers do not
// authenticate the caller themselves.
func (h *Handlers) WriteMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /users/lookup", h.handleLookup)
	mux.HandleFunc("POST /users/presence", h.handlePresence)
	return mux
}

// ReadMux returns the handler for api-read.sock: read-only endpoints
// safe to expose to the logged-in, unprivileged session (mode 0666).
func (h *Handlers) ReadMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/list", h.handleList)
	mux.HandleFunc("GET /users/online", h.handleOnline)
	mux.HandleFunc("GET /users/logins", h.handleRecentLogins)
	mux.HandleFunc("GET /weather", h.handleWeather)
	mux.HandleFunc("GET /exam/questions", h.handleListPools)
	mux.HandleFunc("GET /exam/questions/{pool}", h.handleExamRandomQuestion)
	mux.HandleFunc("GET /exam/questions/{pool}/{questionId}", h.handleExamQuestion)
	return mux
}

func (h *Handlers) handleListPools(w http.ResponseWriter, r *http.Request) {
	availablePools := h.Examiner.ListPools()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	body, err := json.Marshal(availablePools)
	if err != nil {
		http.Error(w, "failed to marshal list of pool names", http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(body); err != nil {
		log.Printf("write pool list response: %v", err)
	}
}

func (h *Handlers) handleExamQuestion(w http.ResponseWriter, r *http.Request) {
	poolName := r.PathValue("pool")
	questionId := r.PathValue("questionId")
	log.Printf("looking for question %s in pool %s", questionId, poolName)
	pool, err := h.Examiner.GetPool(poolName)
	if err != nil {
		http.Error(w, "no such pool", http.StatusNotFound)
		return
	}

	question, err := pool.GetQuestionByID(questionId)
	if err != nil {
		http.Error(w, "no such question", http.StatusNotFound)
		return
	}

	writeQuestionJSON(w, question)
}

func (h *Handlers) handleExamRandomQuestion(w http.ResponseWriter, r *http.Request) {
	poolName := r.PathValue("pool")
	log.Printf("looking for random question in pool %s", poolName)

	pool, err := h.Examiner.GetPool(poolName)
	if err != nil {
		http.Error(w, "no such pool", http.StatusNotFound)
		return
	}

	question := pool.RandomQuestionWithoutFigure()
	if question == nil {
		http.Error(w, "no available questions", http.StatusNotFound)
		return
	}

	writeQuestionJSON(w, question)
}

func writeQuestionJSON(w http.ResponseWriter, question *exam.Question) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	body, err := json.Marshal(question)
	if err != nil {
		http.Error(w, "failed to marshal question", http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(body); err != nil {
		log.Printf("write question response: %v", err)
	}
}

func (h *Handlers) handleWeather(w http.ResponseWriter, r *http.Request) {
	if body, ok := h.cachedWeather(); ok {
		writeWeatherJSON(w, body)
		return
	}

	weatherURL := h.WeatherURL
	if weatherURL == "" {
		weatherURL = DefaultWeatherURL
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, weatherURL, nil)
	if err != nil {
		log.Printf("create weather request: %v", err)
		http.Error(w, "weather service unavailable", http.StatusBadGateway)
		return
	}
	userAgent := h.WeatherUserAgent
	if userAgent == "" {
		userAgent = DefaultWeatherUserAgent
	}
	req.Header.Set("Accept", "application/geo+json, application/json")
	req.Header.Set("User-Agent", userAgent)

	client := h.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("fetch weather: %v", err)
		http.Error(w, "weather service unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWeatherResponseSize+1))
	if err != nil {
		log.Printf("read weather response: %v", err)
		http.Error(w, "weather service unavailable", http.StatusBadGateway)
		return
	}
	if len(body) > maxWeatherResponseSize {
		log.Printf("weather response exceeds %d bytes", maxWeatherResponseSize)
		http.Error(w, "weather response too large", http.StatusBadGateway)
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		log.Printf("weather service returned HTTP %d: %s", resp.StatusCode, summarize(body))
		http.Error(w, "weather service unavailable", http.StatusBadGateway)
		return
	}
	if !json.Valid(body) {
		log.Printf("weather service returned invalid JSON")
		http.Error(w, "weather service returned invalid JSON", http.StatusBadGateway)
		return
	}

	if h.WeatherCacheTTL > 0 {
		h.weatherCacheMu.Lock()
		h.weatherCacheBody = append(h.weatherCacheBody[:0], body...)
		h.weatherCacheUntil = time.Now().Add(h.WeatherCacheTTL)
		h.weatherCacheMu.Unlock()
	}
	writeWeatherJSON(w, body)
}

func (h *Handlers) cachedWeather() ([]byte, bool) {
	if h.WeatherCacheTTL <= 0 {
		return nil, false
	}

	h.weatherCacheMu.Lock()
	defer h.weatherCacheMu.Unlock()
	if len(h.weatherCacheBody) == 0 || !time.Now().Before(h.weatherCacheUntil) {
		return nil, false
	}
	return append([]byte(nil), h.weatherCacheBody...), true
}

func writeWeatherJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		log.Printf("write weather response: %v", err)
	}
}

func summarize(body []byte) string {
	const maxSummary = 256
	if len(body) > maxSummary {
		body = body[:maxSummary]
	}
	return fmt.Sprintf("%q", string(body))
}

type lookupRequest struct {
	Callsign string `json:"callsign"`
}

type lookupResponse struct {
	Callsign string `json:"callsign"`
	UID      int64  `json:"uid"`
	Created  bool   `json:"created"`
}

func (h *Handlers) handleLookup(w http.ResponseWriter, r *http.Request) {
	var req lookupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Callsign == "" {
		http.Error(w, "callsign is required", http.StatusBadRequest)
		return
	}

	user, created, err := h.Store.GetOrCreate(r.Context(), req.Callsign)
	if err != nil {
		log.Printf("lookup %q: %v", req.Callsign, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Idempotent: also self-heals a user whose directories were removed
	// out from under an otherwise-existing account.
	if err := h.Provisioner.EnsureUserDirs(user.UID); err != nil {
		log.Printf("provision uid %d: %v", user.UID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Lookup runs exactly once per session, at login (see
	// container-entrypoint.sh step 2), so this is the login event itself.
	if err := h.Store.RecordLogin(r.Context(), user.UID, user.Callsign); err != nil {
		log.Printf("record login for %q: %v", user.Callsign, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, lookupResponse{
		Callsign: user.Callsign,
		UID:      user.UID,
		Created:  created,
	})
}

type presenceRequest struct {
	Callsign string `json:"callsign"`
	UID      int64  `json:"uid"`
}

// handlePresence reads a single registration line, then hijacks the
// underlying connection and hands it to the presence tracker, which
// blocks on it for as long as the caller keeps it open (see DESIGN.md
// §4.2). No HTTP response is ever written; the connection itself, not a
// status code, is the protocol.
func (h *Handlers) handlePresence(w http.ResponseWriter, r *http.Request) {
	var req presenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Callsign == "" {
		http.Error(w, "callsign is required", http.StatusBadRequest)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("presence hijack for %q: %v", req.Callsign, err)
		return
	}
	defer conn.Close()

	h.Presence.Register(conn, presence.Session{Callsign: req.Callsign, UID: req.UID})
}

func (h *Handlers) handleList(w http.ResponseWriter, r *http.Request) {
	users, err := h.Store.List(r.Context())
	if err != nil {
		log.Printf("list users: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type userInfo struct {
		Callsign  string `json:"callsign"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]userInfo, len(users))
	for i, u := range users {
		out[i] = userInfo{Callsign: u.Callsign, CreatedAt: u.CreatedAt}
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) handleOnline(w http.ResponseWriter, r *http.Request) {
	sessions := h.Presence.Online()

	type onlineInfo struct {
		Callsign string `json:"callsign"`
	}
	out := make([]onlineInfo, len(sessions))
	for i, s := range sessions {
		out[i] = onlineInfo{Callsign: s.Callsign}
	}

	writeJSON(w, http.StatusOK, out)
}

// handleRecentLogins serves the `last` command: the most recent
// recentLoginsLimit login history rows, newest first.
func (h *Handlers) handleRecentLogins(w http.ResponseWriter, r *http.Request) {
	logins, err := h.Store.RecentLogins(r.Context(), recentLoginsLimit)
	if err != nil {
		log.Printf("recent logins: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type loginInfo struct {
		Callsign   string `json:"callsign"`
		LoggedInAt string `json:"logged_in_at"`
	}
	out := make([]loginInfo, len(logins))
	for i, l := range logins {
		out[i] = loginInfo{Callsign: l.Callsign, LoggedInAt: l.LoggedInAt}
	}

	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
