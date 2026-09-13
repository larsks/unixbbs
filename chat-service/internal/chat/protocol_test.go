package chat

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// testClient reads lines from a Handle-driven connection with a
// deadline, so a bug that stalls delivery fails the test instead of
// hanging it.
type testClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func newTestClient(t *testing.T, conn net.Conn) *testClient {
	return &testClient{t: t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *testClient) send(line string) {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		c.t.Fatalf("write %q: %v", line, err)
	}
}

func (c *testClient) recv() string {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := c.r.ReadString('\n')
	if err != nil {
		c.t.Fatalf("read line: %v", err)
	}
	return strings.TrimRight(line, "\n")
}

// recvNone asserts that no line arrives within a short window.
func (c *testClient) recvNone() {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if line, err := c.r.ReadString('\n'); err == nil {
		c.t.Fatalf("expected no line, got %q", strings.TrimRight(line, "\n"))
	}
}

func TestHandleEndToEndGeneralPrivateChatAndBlock(t *testing.T) {
	hub := NewHub()

	aliceConn, aliceSrv := net.Pipe()
	go Handle(aliceSrv, "ALICE", 10001, hub)
	alice := newTestClient(t, aliceConn)
	if got := alice.recv(); !strings.Contains(got, "welcome") {
		t.Fatalf("alice welcome = %q", got)
	}

	bobConn, bobSrv := net.Pipe()
	go Handle(bobSrv, "BOB", 10002, hub)
	bob := newTestClient(t, bobConn)
	if got := bob.recv(); !strings.Contains(got, "welcome") {
		t.Fatalf("bob welcome = %q", got)
	}

	if got := alice.recv(); got != "*** BOB has joined" {
		t.Fatalf("alice got %q, want BOB's join notice", got)
	}

	// General broadcast reaches bob but not back to alice herself.
	alice.send("hello everyone")
	if got := bob.recv(); got != "ALICE: hello everyone" {
		t.Fatalf("bob got %q, want ALICE's broadcast", got)
	}

	// /chat only redirects alice's own outgoing lines (DESIGN.md §9.2):
	// bob gets an invite notice but isn't pulled into anything himself.
	alice.send("/chat BOB")
	if got := bob.recv(); !strings.Contains(got, "ALICE wants to chat with you") {
		t.Fatalf("bob got %q, want private-chat invite", got)
	}
	if got := alice.recv(); !strings.Contains(got, "now sending to BOB") {
		t.Fatalf("alice got %q, want /chat confirmation", got)
	}

	alice.send("hi privately")
	if got := bob.recv(); got != "[private] ALICE: hi privately" {
		t.Fatalf("bob got %q, want ALICE's private message", got)
	}

	// Bob hasn't reciprocated with his own /chat, so his plain reply
	// still broadcasts to general -- and nobody else is there to see
	// it, so alice does not receive it either.
	bob.send("hi back")
	alice.recvNone()

	// Once bob reciprocates, his replies reach alice privately too.
	bob.send("/chat ALICE")
	if got := alice.recv(); !strings.Contains(got, "BOB wants to chat with you") {
		t.Fatalf("alice got %q, want BOB's invite", got)
	}
	bob.recv() // discard bob's own "/chat" confirmation
	bob.send("hi back for real")
	if got := alice.recv(); got != "[private] BOB: hi back for real" {
		t.Fatalf("alice got %q, want BOB's private reply", got)
	}

	// Unknown command: leading "/" is reserved, no escaping.
	alice.send("/etc/foo is broken")
	if got := alice.recv(); got != "no such command: /etc/foo" {
		t.Fatalf("alice got %q, want no-such-command error", got)
	}

	// Bob blocks alice: her /msg to him now fails exactly like he's
	// offline, and he never sees that an attempt was made.
	bob.send("/block ALICE")
	if got := bob.recv(); got != "*** blocking ALICE" {
		t.Fatalf("bob got %q, want block confirmation", got)
	}
	alice.send("/msg BOB should not arrive")
	if got := alice.recv(); got != "BOB is not connected to chat" {
		t.Fatalf("alice got %q, want blocked-as-offline error", got)
	}

	// /quit says goodbye, then the connection closes.
	alice.send("/quit")
	if got := alice.recv(); got != "*** goodbye" {
		t.Fatalf("alice got %q, want goodbye", got)
	}
}

func TestHandleJoinRejectsDuplicateCallsign(t *testing.T) {
	hub := NewHub()

	firstConn, firstSrv := net.Pipe()
	go Handle(firstSrv, "N0CALL", 10001, hub)
	first := newTestClient(t, firstConn)
	first.recv() // welcome

	secondConn, secondSrv := net.Pipe()
	go Handle(secondSrv, "N0CALL", 10001, hub)
	second := newTestClient(t, secondConn)
	if got := second.recv(); !strings.Contains(got, "already connected") {
		t.Fatalf("second session got %q, want already-connected error", got)
	}
}
