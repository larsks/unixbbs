// Package identity resolves a chat-service connection to the callsign
// that's allowed to speak on it, without any client-supplied login step.
// See DESIGN.md §9.1: every ephemeral container's session already runs
// as a unique, per-login UID by the time chat could be invoked, so the
// kernel's own record of "who connected" (SO_PEERCRED) is the entire
// identity mechanism -- a session cannot claim a callsign other than
// the one its own login already established.
package identity

import (
	"context"
	"database/sql"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// PeerUID returns the UID of the process on the other end of conn, read
// directly from the kernel via SO_PEERCRED. This only works for
// AF_UNIX sockets -- conn must be a *net.UnixConn.
func PeerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("syscall conn: %w", err)
	}

	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("control: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("getsockopt SO_PEERCRED: %w", credErr)
	}

	return cred.Uid, nil
}

// CallsignForUID resolves uid to its callsign via a read-only query
// against bbs.db -- the same users table user-service owns (DESIGN.md
// §3), read the same way mail-service already reads it (§4), never
// written to from here.
func CallsignForUID(ctx context.Context, db *sql.DB, uid uint32) (string, error) {
	var callsign string
	row := db.QueryRowContext(ctx, `SELECT callsign FROM users WHERE uid = ?1;`, uid)
	if err := row.Scan(&callsign); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no user with uid %d", uid)
		}
		return "", fmt.Errorf("query uid %d: %w", uid, err)
	}
	return callsign, nil
}
