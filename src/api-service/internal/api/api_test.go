package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"unixbbs/src/api-service/internal/presence"
	"unixbbs/src/api-service/internal/store"
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

func TestHandleLookupRecordsLogin(t *testing.T) {
	h := newTestHandlers(t)
	writeClient, _ := testServer(t, h.WriteMux())
	readClient, _ := testServer(t, h.ReadMux())

	body, _ := json.Marshal(lookupRequest{Callsign: "N0CALL"})
	for i := range 2 {
		resp, err := writeClient.Post("http://unix/users/lookup", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST /users/lookup (call %d): %v", i, err)
		}
		resp.Body.Close()
	}

	loginsResp, err := readClient.Get("http://unix/users/logins")
	if err != nil {
		t.Fatalf("GET /users/logins: %v", err)
	}
	defer loginsResp.Body.Close()

	var logins []struct {
		Callsign string `json:"callsign"`
	}
	if err := json.NewDecoder(loginsResp.Body).Decode(&logins); err != nil {
		t.Fatalf("decode /users/logins: %v", err)
	}
	if len(logins) != 2 {
		t.Fatalf("len(logins) = %d, want 2 (one per lookup call)", len(logins))
	}
	for i, l := range logins {
		if l.Callsign != "N0CALL" {
			t.Errorf("logins[%d].Callsign = %q, want %q", i, l.Callsign, "N0CALL")
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

func TestHandleWeatherReturnsConfiguredForecast(t *testing.T) {
	const forecast = `{"properties":{"periods":[{"name":"Today","isDaytime":true,"temperature":72}]}}`
	weatherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("User-Agent") == "" {
			t.Errorf("weather request = %s User-Agent=%q", r.Method, r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/geo+json")
		ioWriteString(t, w, forecast)
	}))
	defer weatherServer.Close()

	h := newTestHandlers(t)
	h.WeatherURL = weatherServer.URL
	h.HTTPClient = weatherServer.Client()
	client, _ := testServer(t, h.ReadMux())

	resp, err := client.Get("http://unix/weather")
	if err != nil {
		t.Fatalf("GET /weather: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var got struct {
		Properties struct {
			Periods []struct {
				Name string `json:"name"`
			} `json:"periods"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode /weather: %v", err)
	}
	if len(got.Properties.Periods) != 1 || got.Properties.Periods[0].Name != "Today" {
		t.Fatalf("unexpected forecast: %+v", got.Properties.Periods)
	}
}

func TestHandleWeatherCachesSuccessfulResponse(t *testing.T) {
	var requests atomic.Int32
	weatherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		ioWriteString(t, w, `{"properties":{"periods":[]}}`)
	}))
	defer weatherServer.Close()

	h := newTestHandlers(t)
	h.WeatherURL = weatherServer.URL
	h.HTTPClient = weatherServer.Client()
	h.WeatherCacheTTL = time.Hour
	client, _ := testServer(t, h.ReadMux())

	for i := 0; i < 2; i++ {
		resp, err := client.Get("http://unix/weather")
		if err != nil {
			t.Fatalf("GET /weather (%d): %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /weather (%d) status = %d, want %d", i, resp.StatusCode, http.StatusOK)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("upstream requests = %d, want 1", got)
	}
}

func TestHandleWeatherCacheCanBeDisabled(t *testing.T) {
	var requests atomic.Int32
	weatherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		ioWriteString(t, w, `{"properties":{"periods":[]}}`)
	}))
	defer weatherServer.Close()

	h := newTestHandlers(t)
	h.WeatherURL = weatherServer.URL
	h.HTTPClient = weatherServer.Client()
	client, _ := testServer(t, h.ReadMux())

	for i := 0; i < 2; i++ {
		resp, err := client.Get("http://unix/weather")
		if err != nil {
			t.Fatalf("GET /weather (%d): %v", i, err)
		}
		resp.Body.Close()
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("upstream requests = %d, want 2 with caching disabled", got)
	}
}

func TestHandleWeatherRejectsUpstreamFailureAndInvalidJSON(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "upstream status", statusCode: http.StatusServiceUnavailable, body: `{"error":"down"}`},
		{name: "invalid json", statusCode: http.StatusOK, body: "not json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weatherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				ioWriteString(t, w, tt.body)
			}))
			defer weatherServer.Close()

			h := newTestHandlers(t)
			h.WeatherURL = weatherServer.URL
			h.HTTPClient = weatherServer.Client()
			client, _ := testServer(t, h.ReadMux())

			resp, err := client.Get("http://unix/weather")
			if err != nil {
				t.Fatalf("GET /weather: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
			}
		})
	}
}

func ioWriteString(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := strings.NewReader(body).WriteTo(w); err != nil {
		t.Errorf("write test response: %v", err)
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
