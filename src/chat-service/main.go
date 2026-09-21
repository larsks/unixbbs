// Command chat-service implements the BBS's line-oriented chat room.
// See DESIGN.md §9 for the full design.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	_ "modernc.org/sqlite"

	"unixbbs/src/chat-service/internal/chat"
	"unixbbs/src/chat-service/internal/identity"
)

func main() {
	dbPath := flag.String("db", "bbs.db", "path to the read-only SQLite user database (see DESIGN.md §9.1)")
	sockPath := flag.String("socket", "run/chat.sock", "path to the world-connectable chat socket")
	flag.Parse()

	// mode=ro enforces at the driver level what mail-service already
	// treats as a software invariant (compose.yaml): chat-service never
	// writes to bbs.db, only api-service does (DESIGN.md §4/§9.4).
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", *dbPath))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	listener, err := listenUnix(*sockPath, 0o666)
	if err != nil {
		log.Fatalf("listen on chat socket: %v", err)
	}
	defer listener.Close()

	hub := chat.NewHub()

	log.Printf("chat-service listening: socket=%s db=%s", *sockPath, *dbPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalf("accept: %v", err)
		}
		go handleConn(conn, db, hub)
	}
}

func handleConn(conn net.Conn, db *sql.DB, hub *chat.Hub) {
	defer conn.Close()

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		log.Printf("chat connection is not a Unix socket: %T", conn)
		return
	}

	uid, err := identity.PeerUID(unixConn)
	if err != nil {
		log.Printf("get peer credentials: %v", err)
		return
	}

	callsign, err := identity.CallsignForUID(context.Background(), db, uid)
	if err != nil {
		log.Printf("resolve uid %d: %v", uid, err)
		fmt.Fprintln(conn, "unable to resolve your identity; contact the sysop")
		return
	}

	chat.Handle(conn, callsign, int64(uid), hub)
}

// listenUnix binds a Unix-domain socket at path, removing any stale
// socket file left behind by a previous run, and sets its permission
// bits explicitly -- net.Listen's default mode depends on umask, which
// is not something to leave implicit for a socket everyone connects to
// (see DESIGN.md §9.1/§9.4). Mirrors api-service's listenUnix.
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
