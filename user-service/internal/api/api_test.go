package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"unixbbs/user-service/internal/presence"
	"unixbbs/user-service/internal/store"
)

// stubProvisioner satisfies Provisioner without touching the
// filesystem -- provision.Provisioner's own directory/ownership
// behavior is covered by internal/provision's own tests.
type stubProvisioner struct{}

func (stubProvisioner) EnsureUserDirs(uid int64) error { return nil }

// testServer starts a Handlers-backed HTTP server listening on a Unix
// socket in a temp dir, and returns an *http.Client dialed against it
// plus the Handlers for direct inspection (e.g. the presence tracker).
func testServer(t *testing.T, mux http.Handler) (*http.Client, net.Listener) {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	go http.Serve(l, mux)

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	return client, l
}

func newTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bbs.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	return &Handlers{Store: st, Presence: presence.New(), Provisioner: stubProvisioner{}}
}

func TestHandleLookupCreatesNewUser(t *testing.T) {
	h := newTestHandlers(t)
	client, _ := testServer(t, h.WriteMux())

	body, _ := json.Marshal(lookupRequest{Callsign: "N0CALL"})
	resp, err := client.Post("http://unix/users/lookup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /users/lookup: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var got lookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Callsign != "N0CALL" || !got.Created || got.UID < 10000 {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestHandleLookupSecondCallIsNotCreated(t *testing.T) {
	h := newTestHandlers(t)
	client, _ := testServer(t, h.WriteMux())

	body, _ := json.Marshal(lookupRequest{Callsign: "N0CALL"})
	for i, want := range []bool{true, false} {
		resp, err := client.Post("http://unix/users/lookup", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST /users/lookup (call %d): %v", i, err)
		}
		var got lookupResponse
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode response (call %d): %v", i, err)
		}
		resp.Body.Close()
		if got.Created != want {
			t.Errorf("call %d: created = %v, want %v", i, got.Created, want)
		}
	}
}

func TestHandleListAndOnline(t *testing.T) {
	h := newTestHandlers(t)
	writeClient, _ := testServer(t, h.WriteMux())
	readClient, _ := testServer(t, h.ReadMux())

	body, _ := json.Marshal(lookupRequest{Callsign: "N0CALL"})
	resp, err := writeClient.Post("http://unix/users/lookup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /users/lookup: %v", err)
	}
	resp.Body.Close()

	listResp, err := readClient.Get("http://unix/users/list")
	if err != nil {
		t.Fatalf("GET /users/list: %v", err)
	}
	defer listResp.Body.Close()
	var users []struct {
		Callsign string `json:"callsign"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&users); err != nil {
		t.Fatalf("decode /users/list: %v", err)
	}
	if len(users) != 1 || users[0].Callsign != "N0CALL" {
		t.Fatalf("unexpected /users/list result: %+v", users)
	}

	// Nobody has registered presence yet.
	onlineResp, err := readClient.Get("http://unix/users/online")
	if err != nil {
		t.Fatalf("GET /users/online: %v", err)
	}
	var online []struct {
		Callsign string `json:"callsign"`
	}
	if err := json.NewDecoder(onlineResp.Body).Decode(&online); err != nil {
		t.Fatalf("decode /users/online: %v", err)
	}
	onlineResp.Body.Close()
	if len(online) != 0 {
		t.Fatalf("/users/online = %+v, want empty before any presence registration", online)
	}
}

func TestHandlePresenceRegistersUntilConnectionCloses(t *testing.T) {
	h := newTestHandlers(t)
	_, listener := testServer(t, h.WriteMux())

	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	body, _ := json.Marshal(presenceRequest{Callsign: "N0CALL", UID: 10042})
	req := "POST /users/presence HTTP/1.1\r\n" +
		"Host: unix\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write request line: %v", err)
	}
	if _, err := conn.Write(body); err != nil {
		t.Fatalf("write request body: %v", err)
	}

	// Give the server a moment to process the registration and hijack
	// the connection.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.Presence.Online()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	online := h.Presence.Online()
	if len(online) != 1 || online[0].Callsign != "N0CALL" || online[0].UID != 10042 {
		t.Fatalf("Online() = %+v, want one N0CALL/10042 session", online)
	}

	// Closing the connection should remove the session.
	conn.Close()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.Presence.Online()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Online() = %+v, want empty after connection close", h.Presence.Online())
}
