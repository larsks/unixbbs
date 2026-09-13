package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "bbs.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestGetOrCreateNewUser(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	u, created, err := st.GetOrCreate(ctx, "N0CALL")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if !created {
		t.Error("expected created=true for a brand-new callsign")
	}
	if u.Callsign != "N0CALL" {
		t.Errorf("callsign = %q, want %q", u.Callsign, "N0CALL")
	}
	if u.UID < 10000 {
		t.Errorf("uid = %d, want >= 10000 (sequence primed at 9999)", u.UID)
	}
}

func TestGetOrCreateExistingUser(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	first, _, err := st.GetOrCreate(ctx, "N0CALL")
	if err != nil {
		t.Fatalf("GetOrCreate (first): %v", err)
	}

	second, created, err := st.GetOrCreate(ctx, "N0CALL")
	if err != nil {
		t.Fatalf("GetOrCreate (second): %v", err)
	}
	if created {
		t.Error("expected created=false for a pre-existing callsign")
	}
	if second.UID != first.UID {
		t.Errorf("uid changed across lookups: %d != %d", second.UID, first.UID)
	}
}

func TestGetOrCreateIsCaseInsensitive(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	lower, _, err := st.GetOrCreate(ctx, "n0call")
	if err != nil {
		t.Fatalf("GetOrCreate (lower): %v", err)
	}

	upper, created, err := st.GetOrCreate(ctx, "N0CALL")
	if err != nil {
		t.Fatalf("GetOrCreate (upper): %v", err)
	}
	if created {
		t.Error("expected created=false: N0CALL should match existing n0call")
	}
	if upper.UID != lower.UID {
		t.Errorf("uid differs by case: %d != %d", upper.UID, lower.UID)
	}
	// The stored callsign should be whichever spelling was inserted first.
	if upper.Callsign != "n0call" {
		t.Errorf("callsign = %q, want the original spelling %q", upper.Callsign, "n0call")
	}
}

func TestRecordAndRecentLogins(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for _, cs := range []string{"N0CALL", "W1AW", "K2ABC"} {
		if err := st.RecordLogin(ctx, 10000, cs); err != nil {
			t.Fatalf("RecordLogin(%q): %v", cs, err)
		}
	}

	logins, err := st.RecentLogins(ctx, 10)
	if err != nil {
		t.Fatalf("RecentLogins: %v", err)
	}
	if len(logins) != 3 {
		t.Fatalf("len(logins) = %d, want 3", len(logins))
	}
	// Newest first.
	want := []string{"K2ABC", "W1AW", "N0CALL"}
	for i, l := range logins {
		if l.Callsign != want[i] {
			t.Errorf("logins[%d].Callsign = %q, want %q", i, l.Callsign, want[i])
		}
	}
}

func TestRecentLoginsRespectsLimit(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for range 5 {
		if err := st.RecordLogin(ctx, 10000, "N0CALL"); err != nil {
			t.Fatalf("RecordLogin: %v", err)
		}
	}

	logins, err := st.RecentLogins(ctx, 2)
	if err != nil {
		t.Fatalf("RecentLogins: %v", err)
	}
	if len(logins) != 2 {
		t.Fatalf("len(logins) = %d, want 2", len(logins))
	}
}

func TestExpireLogins(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.RecordLogin(ctx, 10000, "N0CALL"); err != nil {
		t.Fatalf("RecordLogin (recent): %v", err)
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO logins (uid, callsign, logged_in_at) VALUES (?, ?, datetime('now', '-15 days'));`,
		10001, "W1AW",
	); err != nil {
		t.Fatalf("insert stale login: %v", err)
	}

	n, err := st.ExpireLogins(ctx)
	if err != nil {
		t.Fatalf("ExpireLogins: %v", err)
	}
	if n != 1 {
		t.Fatalf("ExpireLogins deleted %d rows, want 1", n)
	}

	logins, err := st.RecentLogins(ctx, 10)
	if err != nil {
		t.Fatalf("RecentLogins: %v", err)
	}
	if len(logins) != 1 || logins[0].Callsign != "N0CALL" {
		t.Fatalf("RecentLogins after expiry = %+v, want only the recent N0CALL row", logins)
	}
}

func TestList(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for _, cs := range []string{"N0CALL", "W1AW", "K2ABC"} {
		if _, _, err := st.GetOrCreate(ctx, cs); err != nil {
			t.Fatalf("GetOrCreate(%q): %v", cs, err)
		}
	}

	users, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("len(users) = %d, want 3", len(users))
	}
	// Ordered by uid, i.e. insertion order.
	want := []string{"N0CALL", "W1AW", "K2ABC"}
	for i, u := range users {
		if u.Callsign != want[i] {
			t.Errorf("users[%d].Callsign = %q, want %q", i, u.Callsign, want[i])
		}
	}
}
