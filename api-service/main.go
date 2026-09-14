// Command api-service owns the BBS API, account database, and presence
// tracking. See DESIGN.md §4 for the full design.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"unixbbs/api-service/internal/api"
	"unixbbs/api-service/internal/presence"
	"unixbbs/api-service/internal/provision"
	"unixbbs/api-service/internal/store"
)

func main() {
	dbPath := flag.String("db", "bbs.db", "path to the SQLite user database")
	dataDir := flag.String("data-dir", ".", "path to the shared data root containing mail/ and home/ (see DESIGN.md §3)")
	writeSockPath := flag.String("write-socket", "run/api-write.sock", "path to the root-only write socket")
	readSockPath := flag.String("read-socket", "run/api-read.sock", "path to the world-connectable read socket")
	weatherURL := flag.String("weather-url", api.DefaultWeatherURL, "URL of the Weather.gov forecast endpoint")
	weatherUserAgent := flag.String("weather-user-agent", api.DefaultWeatherUserAgent, "User-Agent sent to the weather endpoint")
	weatherCacheTTLSeconds := flag.Int64("weather-cache-ttl", 1800, "weather response cache TTL in seconds; 0 disables caching")
	flag.Parse()
	if err := validateHTTPURL(*weatherURL); err != nil {
		log.Fatalf("invalid weather URL: %v", err)
	}
	if *weatherCacheTTLSeconds < 0 {
		log.Fatalf("invalid weather cache TTL: must be zero or greater")
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()

	provisioner := provision.New(*dataDir)
	if err := provisioner.EnsureNewsDir(); err != nil {
		log.Fatalf("ensure news dir: %v", err)
	}

	handlers := &api.Handlers{
		Store:            st,
		Presence:         presence.New(),
		Provisioner:      provisioner,
		WeatherURL:       *weatherURL,
		HTTPClient:       &http.Client{Timeout: 15 * time.Second},
		WeatherUserAgent: *weatherUserAgent,
		WeatherCacheTTL:  time.Duration(*weatherCacheTTLSeconds) * time.Second,
	}

	writeListener, err := listenUnix(*writeSockPath, 0o700)
	if err != nil {
		log.Fatalf("listen on write socket: %v", err)
	}
	defer writeListener.Close()

	readListener, err := listenUnix(*readSockPath, 0o666)
	if err != nil {
		log.Fatalf("listen on read socket: %v", err)
	}
	defer readListener.Close()

	errCh := make(chan error, 2)
	go func() {
		errCh <- http.Serve(writeListener, handlers.WriteMux())
	}()
	go func() {
		errCh <- http.Serve(readListener, handlers.ReadMux())
	}()

	log.Printf("api-service listening: write=%s read=%s db=%s weather=%s cache_ttl=%s", *writeSockPath, *readSockPath, *dbPath, *weatherURL, time.Duration(*weatherCacheTTLSeconds)*time.Second)
	log.Fatal(<-errCh)
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("must be an absolute http or https URL")
	}
	if u.User != nil {
		return fmt.Errorf("must not contain userinfo")
	}
	return nil
}

// listenUnix binds a Unix-domain socket at path, removing any stale
// socket file left behind by a previous run, and sets its permission
// bits explicitly -- net.Listen's default mode depends on umask, which
// is not something to leave implicit for the socket that enforces the
// root/unprivileged trust boundary (see DESIGN.md §4.1).
func listenUnix(path string, mode os.FileMode) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(path, mode); err != nil {
		l.Close()
		return nil, err
	}

	return l, nil
}

// touch Sun Sep 13 08:25:53 AM EDT 2026
