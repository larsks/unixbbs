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
