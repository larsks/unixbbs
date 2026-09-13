CREATE TABLE IF NOT EXISTS users (
  uid        INTEGER PRIMARY KEY AUTOINCREMENT,
  callsign   TEXT UNIQUE NOT NULL COLLATE NOCASE,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- prime the sequence so real users start at 10000, clear of any
-- container-image system UID range.
INSERT OR IGNORE INTO sqlite_sequence (name, seq) VALUES ('users', 9999);

-- One row per completed login (see api.handleLookup, called once per
-- session at container start). Used by the `last` command and expired
-- by a daily cron job after 14 days.
CREATE TABLE IF NOT EXISTS logins (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  uid          INTEGER NOT NULL,
  callsign     TEXT NOT NULL,
  logged_in_at TEXT NOT NULL DEFAULT (datetime('now'))
);
