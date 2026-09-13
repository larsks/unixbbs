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
