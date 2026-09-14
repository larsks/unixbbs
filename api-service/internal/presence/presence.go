// Package presence tracks which callsigns are currently connected.
//
// A session is "present" for exactly as long as its registration
// connection (see Register) stays open. There is no heartbeat and
// nothing is persisted: the OS-level "this connection is broken" signal
// is the liveness signal, so a crashed or killed container is detected
// the same way a clean logout is.
package presence

import (
	"io"
	"net"
	"sync"
)

// Session describes one connected user.
type Session struct {
	Callsign string
	UID      int64
}

// Tracker holds the set of currently-connected sessions.
type Tracker struct {
	mu       sync.Mutex
	sessions map[net.Conn]Session
}

// New returns an empty Tracker.
func New() *Tracker {
	return &Tracker{sessions: make(map[net.Conn]Session)}
}

// Register adds sess to the tracked set and blocks until conn is closed
// or produces an error, at which point sess is removed. Callers should
// run Register in its own goroutine for the lifetime of conn.
func (t *Tracker) Register(conn net.Conn, sess Session) {
	t.mu.Lock()
	t.sessions[conn] = sess
	t.mu.Unlock()

	// Block until the peer closes the connection (or the connection
	// otherwise fails) -- the read itself is never expected to produce
	// data.
	_, _ = io.Copy(io.Discard, conn)

	t.mu.Lock()
	delete(t.sessions, conn)
	t.mu.Unlock()
}

// Online returns the currently-connected sessions.
func (t *Tracker) Online() []Session {
	t.mu.Lock()
	defer t.mu.Unlock()

	sessions := make([]Session, 0, len(t.sessions))
	for _, sess := range t.sessions {
		sessions = append(sessions, sess)
	}
	return sessions
}
