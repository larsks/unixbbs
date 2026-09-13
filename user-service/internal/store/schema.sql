CREATE TABLE IF NOT EXISTS users (
  uid        INTEGER PRIMARY KEY AUTOINCREMENT,
  callsign   TEXT UNIQUE NOT NULL COLLATE NOCASE,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- prime the sequence so real users start at 10000, clear of any
-- container-image system UID range.
INSERT OR IGNORE INTO sqlite_sequence (name, seq) VALUES ('users', 9999);
