package provision

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Chowning to an arbitrary uid requires root, which the test
// environment may not have. Chowning to the test process's own uid,
// however, is always permitted -- that's enough to exercise
// EnsureUserDirs' mkdir+chown logic without requiring root.
var testUID = int64(os.Getuid())

func TestEnsureUserDirsCreatesMaildirAndHome(t *testing.T) {
	dir := t.TempDir()
	p := New(dir)
	uidStr := strconv.FormatInt(testUID, 10)

	if err := p.EnsureUserDirs(testUID); err != nil {
		t.Fatalf("EnsureUserDirs: %v", err)
	}

	for _, sub := range []string{"cur", "new", "tmp"} {
		path := filepath.Join(dir, "mail", uidStr, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", path)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 0700", path, info.Mode().Perm())
		}
	}

	home := filepath.Join(dir, "home", uidStr)
	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat %s: %v", home, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", home)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("%s mode = %o, want 0700", home, info.Mode().Perm())
	}
}

func TestEnsureUserDirsLeavesParentsTraversable(t *testing.T) {
	// Regression test: MkdirAll applies its mode to every intermediate
	// directory it creates, not just the leaf. On a completely fresh
	// data root (mail/ and home/ don't exist yet -- the case a new,
	// empty Docker volume hits on first use), creating the per-uid dir
	// directly with 0700 used to leave mail/ and home/ themselves mode
	// 0700 root:root, which blocks traversal into the correctly-owned
	// per-uid directory underneath for anyone but root.
	dir := t.TempDir()
	p := New(dir)

	if err := p.EnsureUserDirs(testUID); err != nil {
		t.Fatalf("EnsureUserDirs: %v", err)
	}

	for _, top := range []string{"mail", "home"} {
		info, err := os.Stat(filepath.Join(dir, top))
		if err != nil {
			t.Fatalf("stat %s: %v", top, err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s mode = %o, not traversable", top, info.Mode().Perm())
		}
	}
}

func TestEnsureUserDirsIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := New(dir)

	if err := p.EnsureUserDirs(testUID); err != nil {
		t.Fatalf("EnsureUserDirs (first): %v", err)
	}
	if err := p.EnsureUserDirs(testUID); err != nil {
		t.Fatalf("EnsureUserDirs (second): %v", err)
	}
}

func TestEnsureBulletinsDirCreatesMaildir(t *testing.T) {
	dir := t.TempDir()
	p := New(dir)

	if err := p.EnsureBulletinsDir(); err != nil {
		t.Fatalf("EnsureBulletinsDir: %v", err)
	}

	for _, sub := range []string{"cur", "new", "tmp"} {
		path := filepath.Join(dir, "bulletins", sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", path)
		}
	}
}

func TestEnsureBulletinsDirIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := New(dir)

	if err := p.EnsureBulletinsDir(); err != nil {
		t.Fatalf("EnsureBulletinsDir (first): %v", err)
	}
	if err := p.EnsureBulletinsDir(); err != nil {
		t.Fatalf("EnsureBulletinsDir (second): %v", err)
	}
}
