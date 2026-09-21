package identity

import (
	"context"
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// unixSocketPair returns a connected client/server *net.UnixConn pair
// over a real Unix-domain socket in a temp dir -- SO_PEERCRED only
// applies to genuine AF_UNIX sockets, so this can't be faked with
// net.Pipe.
func unixSocketPair(t *testing.T) (client, server *net.UnixConn) {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	acceptCh := make(chan net.Conn, 1)
	acceptErrCh := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			acceptErrCh <- err
			return
		}
		acceptCh <- c
	}()

	c, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	select {
	case s := <-acceptCh:
		t.Cleanup(func() { s.Close() })
		return c.(*net.UnixConn), s.(*net.UnixConn)
	case err := <-acceptErrCh:
		t.Fatalf("accept: %v", err)
	}
	panic("unreachable")
}

func TestPeerUIDIsOwnProcess(t *testing.T) {
	client, server := unixSocketPair(t)
	_ = client

	// Reading the client's credentials from the server side of the
	// connection should report this test process's own UID, since
	// that's who actually dialed.
	uid, err := PeerUID(server)
	if err != nil {
		t.Fatalf("PeerUID: %v", err)
	}
	if uid != uint32(os.Getuid()) {
		t.Errorf("PeerUID = %d, want %d (os.Getuid())", uid, os.Getuid())
	}
}

func TestCallsignForUID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bbs.db")

	setup, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open (setup): %v", err)
	}
	if _, err := setup.Exec(`CREATE TABLE users (uid INTEGER PRIMARY KEY, callsign TEXT NOT NULL COLLATE NOCASE)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := setup.Exec(`INSERT INTO users (uid, callsign) VALUES (10042, 'N0CALL')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	setup.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	callsign, err := CallsignForUID(context.Background(), db, 10042)
	if err != nil {
		t.Fatalf("CallsignForUID: %v", err)
	}
	if callsign != "N0CALL" {
		t.Errorf("callsign = %q, want N0CALL", callsign)
	}

	if _, err := CallsignForUID(context.Background(), db, 99999); err == nil {
		t.Error("CallsignForUID for unknown uid: want error, got nil")
	}
}
