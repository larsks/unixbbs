// Package provision creates and owns the per-user filesystem state
// (Maildir, home directory) that DESIGN.md §5 step 5 requires to exist
// before an ephemeral container provisions its local account. Ephemeral
// containers never create these directories themselves.
package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Provisioner creates per-uid directories under a shared data root.
type Provisioner struct {
	dataDir string
}

// New returns a Provisioner rooted at dataDir (the directory containing
// mail/ and home/, per DESIGN.md §3).
func New(dataDir string) *Provisioner {
	return &Provisioner{dataDir: dataDir}
}

// EnsureUserDirs makes sure uid's Maildir (cur/new/tmp) and home
// directory exist, mode 0700, owned by uid:uid. It's safe to call on
// every login, not just first login: idempotent, and self-healing if
// the directories were ever removed out from under an existing account.
func (p *Provisioner) EnsureUserDirs(uid int64) error {
	uidStr := strconv.FormatInt(uid, 10)

	// mail/ and home/ themselves must stay traversable (0755): MkdirAll
	// applies its mode argument to every intermediate directory it has
	// to create, not just the leaf, so creating the per-uid dir directly
	// with 0700 on a fresh data root left mail/ and home/ themselves
	// mode 0700 root:root -- confirmed by testing (against a fresh,
	// empty Docker volume) to lock every other uid out of its own,
	// correctly-owned 0700 subdirectory, since traversal requires the
	// parent's execute bit too. Creating these two parents explicitly
	// first, before the per-uid MkdirAll below, avoids that.
	if err := os.MkdirAll(filepath.Join(p.dataDir, "mail"), 0o755); err != nil {
		return fmt.Errorf("create mail dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(p.dataDir, "home"), 0o755); err != nil {
		return fmt.Errorf("create home dir: %w", err)
	}

	maildir := filepath.Join(p.dataDir, "mail", uidStr)
	if err := os.MkdirAll(maildir, 0o700); err != nil {
		return fmt.Errorf("create maildir %s: %w", maildir, err)
	}
	if err := os.Chown(maildir, int(uid), int(uid)); err != nil {
		return fmt.Errorf("chown %s: %w", maildir, err)
	}
	for _, sub := range []string{"cur", "new", "tmp"} {
		dir := filepath.Join(maildir, sub)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create maildir %s: %w", dir, err)
		}
		if err := os.Chown(dir, int(uid), int(uid)); err != nil {
			return fmt.Errorf("chown %s: %w", dir, err)
		}
	}

	home := filepath.Join(p.dataDir, "home", uidStr)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create home dir %s: %w", home, err)
	}
	if err := os.Chown(home, int(uid), int(uid)); err != nil {
		return fmt.Errorf("chown home dir %s: %w", home, err)
	}

	return nil
}

// EnsureNewsDir makes sure the news Maildir (cur/new/tmp)
// exists under the data root, per DESIGN.md §4's "Bulletins"
// subsection. It's deliberately left owned by whatever this process
// runs as (root, in the real deployment) with no further chown: the
// read-only-fallback behavior documented there depends on the
// unprivileged reader never having write access, which plain directory
// creation under a root process already gives us. Safe to call on every
// startup: idempotent, and self-healing if the directories were ever
// removed from the volume.
func (p *Provisioner) EnsureNewsDir() error {
	for _, sub := range []string{"cur", "new", "tmp"} {
		dir := filepath.Join(p.dataDir, "news", sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create news dir %s: %w", dir, err)
		}
	}
	return nil
}
