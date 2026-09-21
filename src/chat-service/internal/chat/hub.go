// Package chat implements chat-service's in-memory session registry and
// message routing: a single "general" channel plus one-to-one private
// conversations entered and left with /chat and /leave. See DESIGN.md
// §9.2/§9.3 for the full protocol this implements.
package chat

import (
	"fmt"
	"sort"
	"sync"
)

// Session is one connected chat client, identified by the callsign
// resolved for it in internal/identity -- never anything the client
// itself supplies.
type Session struct {
	Callsign string
	UID      int64

	out chan string
}

// send queues line for delivery to this session without blocking the
// caller: a slow or wedged client must never be able to stall a
// broadcast meant for everyone else. If the session's buffer is full,
// the line is dropped.
func (s *Session) send(line string) {
	select {
	case s.out <- line:
	default:
	}
}

// Hub tracks every currently-connected chat session, each one's current
// target -- "" for the general channel, or another session's callsign
// for a private conversation entered with /chat -- and each one's block
// list.
type Hub struct {
	mu       sync.Mutex
	sessions map[string]*Session
	target   map[string]string
	blocked  map[string]map[string]bool // blocker -> set of blocked callsigns
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{
		sessions: make(map[string]*Session),
		target:   make(map[string]string),
		blocked:  make(map[string]map[string]bool),
	}
}

// isBlockedLocked reports whether blocker has blocked other. Callers
// must already hold h.mu.
func (h *Hub) isBlockedLocked(blocker, other string) bool {
	return h.blocked[blocker][other]
}

// Join adds sess to the hub in the general channel and announces its
// arrival there. It fails if callsign is already connected: /msg and
// /chat both address a single session by callsign, so two simultaneous
// sessions for one callsign would make that addressing ambiguous.
func (h *Hub) Join(sess *Session) error {
	h.mu.Lock()
	if _, exists := h.sessions[sess.Callsign]; exists {
		h.mu.Unlock()
		return fmt.Errorf("%s is already connected to chat", sess.Callsign)
	}
	h.sessions[sess.Callsign] = sess
	h.target[sess.Callsign] = ""
	h.mu.Unlock()

	h.announce(sess.Callsign, "", fmt.Sprintf("*** %s has joined", sess.Callsign))
	return nil
}

// Leave removes callsign from the hub (see DESIGN.md §9.3). Anyone whose
// private conversation was with callsign is moved back to general;
// anyone who could otherwise see callsign leave (i.e. everyone
// currently in general, or -- via the orphan path -- pointed at
// callsign) gets a plain leave notice, unless they'd blocked callsign,
// in which case they're still retargeted to general but not notified:
// /block's whole point is to stop hearing from someone, including
// hearing that they left.
func (h *Hub) Leave(callsign string) {
	h.mu.Lock()
	if _, ok := h.sessions[callsign]; !ok {
		h.mu.Unlock()
		return
	}
	leavingTarget := h.target[callsign]
	delete(h.sessions, callsign)
	delete(h.target, callsign)

	var orphaned []*Session
	for other, target := range h.target {
		if target == callsign {
			h.target[other] = ""
			if !h.isBlockedLocked(other, callsign) {
				orphaned = append(orphaned, h.sessions[other])
			}
		}
	}
	h.mu.Unlock()

	for _, o := range orphaned {
		o.send(fmt.Sprintf("*** %s has left; returning to general", callsign))
	}
	h.announce(callsign, leavingTarget, fmt.Sprintf("*** %s has left", callsign))
}

// announce sends line to every session whose current target matches
// location ("" for general), except the one named by exceptCallsign
// (the author of the notice/message) and anyone who has blocked them.
func (h *Hub) announce(exceptCallsign, location, line string) {
	h.mu.Lock()
	var recipients []*Session
	for callsign, sess := range h.sessions {
		if callsign == exceptCallsign {
			continue
		}
		if h.target[callsign] != location {
			continue
		}
		if h.isBlockedLocked(callsign, exceptCallsign) {
			continue
		}
		recipients = append(recipients, sess)
	}
	h.mu.Unlock()

	for _, r := range recipients {
		r.send(line)
	}
}

// Send routes text typed by sess according to its current target:
// broadcast to everyone else in general, or a private message to
// whichever user was entered via /chat. If that private-chat partner
// has since disconnected, sess is bounced back to general with a
// notice -- Leave already retargets anyone left mid-conversation, so
// this is a defensive fallback rather than the normal path.
func (h *Hub) Send(sess *Session, text string) {
	h.mu.Lock()
	target := h.target[sess.Callsign]
	h.mu.Unlock()

	if target == "" {
		h.announce(sess.Callsign, "", fmt.Sprintf("%s: %s", sess.Callsign, text))
		return
	}

	h.mu.Lock()
	recipient, ok := h.sessions[target]
	if !ok {
		h.target[sess.Callsign] = ""
	}
	h.mu.Unlock()

	if !ok {
		sess.send(fmt.Sprintf("*** %s is no longer connected; returning to general", target))
		return
	}
	recipient.send(fmt.Sprintf("[private] %s: %s", sess.Callsign, text))
}

// Msg sends a one-shot private message from sess to target, without
// changing sess's own current conversation (DESIGN.md §9.2's /msg). If
// target has blocked sess, this fails exactly as if target weren't
// connected at all -- deliberately indistinguishable, so a blocked user
// can't tell a block from the target simply being offline.
func (h *Hub) Msg(sess *Session, target, text string) error {
	h.mu.Lock()
	recipient, ok := h.sessions[target]
	blocked := ok && h.isBlockedLocked(target, sess.Callsign)
	h.mu.Unlock()
	if !ok || blocked {
		return fmt.Errorf("%s is not connected to chat", target)
	}
	recipient.send(fmt.Sprintf("[private] %s: %s", sess.Callsign, text))
	return nil
}

// EnterChat redirects sess's own outgoing unprefixed lines to target,
// and lets target know so they can reciprocate with their own /chat.
// It deliberately does NOT retarget target itself: an earlier version
// of this design paired both sides automatically, which meant anyone
// could unilaterally redirect where a stranger's own typing went --
// nothing here can do that now, since a plain reply on target's side
// still goes wherever target has separately chosen to send it (general,
// unless they too run /chat). Retargets sess immediately if it was
// already in some other conversation, with no forced /leave first
// (DESIGN.md §9.2). If target has blocked sess, this fails exactly as
// if target weren't connected at all (see Msg).
func (h *Hub) EnterChat(sess *Session, target string) error {
	h.mu.Lock()
	recipient, ok := h.sessions[target]
	blocked := ok && h.isBlockedLocked(target, sess.Callsign)
	if !ok || blocked {
		h.mu.Unlock()
		return fmt.Errorf("%s is not connected to chat", target)
	}
	h.target[sess.Callsign] = target
	h.mu.Unlock()

	recipient.send(fmt.Sprintf("*** %s wants to chat with you -- /chat %s to join, or /block %s to stop hearing from them",
		sess.Callsign, sess.Callsign, sess.Callsign))
	return nil
}

// Block records that sess no longer wants to hear from target: their
// future /msg and /chat invites are rejected the same way a
// disconnected user would be (see Msg/EnterChat), and their general-
// channel lines (including join/leave notices) are hidden from sess.
// One-directional -- target is unaffected and can still see sess.
func (h *Hub) Block(sess *Session, target string) {
	h.mu.Lock()
	if h.blocked[sess.Callsign] == nil {
		h.blocked[sess.Callsign] = make(map[string]bool)
	}
	h.blocked[sess.Callsign][target] = true
	h.mu.Unlock()
}

// Unblock reverses a previous Block.
func (h *Hub) Unblock(sess *Session, target string) {
	h.mu.Lock()
	delete(h.blocked[sess.Callsign], target)
	h.mu.Unlock()
}

// LeaveChat returns sess to the general channel. It only ever changes
// sess's own target: the partner sess was chatting with is not forced
// out (DESIGN.md §9.3) and keeps addressing sess until they too /leave,
// /chat someone else, or sess disconnects (see Leave's orphan handling).
func (h *Hub) LeaveChat(sess *Session) {
	h.mu.Lock()
	h.target[sess.Callsign] = ""
	h.mu.Unlock()
}

// InGeneral reports whether sess is currently in the general channel
// rather than a private conversation.
func (h *Hub) InGeneral(sess *Session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.target[sess.Callsign] == ""
}

// Who returns the callsigns currently connected to chat, sorted. This is
// deliberately a different roster than the top-level `users` command
// (DESIGN.md §4.2, general BBS presence) -- see /who in §9.2.
func (h *Hub) Who() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.sessions))
	for callsign := range h.sessions {
		out = append(out, callsign)
	}
	sort.Strings(out)
	return out
}
