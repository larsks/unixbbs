// Command user-service owns the BBS's account database and presence
// tracking. See DESIGN.md §4 for the full design.
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"

	"unixbbs/user-service/internal/api"
	"unixbbs/user-service/internal/presence"
	"unixbbs/user-service/internal/provision"
	"unixbbs/user-service/internal/store"
)

func main() {
	dbPath := flag.String("db", "bbs.db", "path to the SQLite user database")
	dataDir := flag.String("data-dir", ".", "path to the shared data root containing mail/ and home/ (see DESIGN.md §3)")
	writeSockPath := flag.String("write-socket", "run/user-write.sock", "path to the root-only write socket")
	readSockPath := flag.String("read-socket", "run/user-read.sock", "path to the world-connectable read socket")
	flag.Parse()

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
		Store:       st,
		Presence:    presence.New(),
		Provisioner: provisioner,
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

	log.Printf("user-service listening: write=%s read=%s db=%s", *writeSockPath, *readSockPath, *dbPath)
	log.Fatal(<-errCh)
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
