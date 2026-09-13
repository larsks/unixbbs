// Package api implements the user-service HTTP handlers, split across
// the two sockets described in DESIGN.md §4.1: a root-only write socket
// (account lookup/creation, presence registration) and a
// world-connectable read socket (listing users, listing who's online).
package api

import (
	"encoding/json"
	"log"
	"net/http"

	"unixbbs/user-service/internal/presence"
	"unixbbs/user-service/internal/store"
)

// Provisioner creates the per-uid directories a newly-looked-up user
// needs (see the internal/provision package). It's an interface here,
// rather than a concrete type, so handler tests don't have to satisfy
// provision.Provisioner's real chown requirements.
type Provisioner interface {
	EnsureUserDirs(uid int64) error
}

// Handlers holds the shared state used by both sockets' handlers.
type Handlers struct {
	Store       *store.Store
	Presence    *presence.Tracker
	Provisioner Provisioner
}

// WriteMux returns the handler for user-write.sock: mutating/
// identity-claiming endpoints only. Callers must serve this on a socket
// only root can connect to (mode 0700) -- these handlers do not
// authenticate the caller themselves.
func (h *Handlers) WriteMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /users/lookup", h.handleLookup)
	mux.HandleFunc("POST /users/presence", h.handlePresence)
	return mux
}

// ReadMux returns the handler for user-read.sock: read-only endpoints
// safe to expose to the logged-in, unprivileged session (mode 0666).
func (h *Handlers) ReadMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/list", h.handleList)
	mux.HandleFunc("GET /users/online", h.handleOnline)
	return mux
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
