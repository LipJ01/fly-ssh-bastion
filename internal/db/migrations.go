package db

const schema = `
CREATE TABLE IF NOT EXISTS machines (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL UNIQUE,
    owner         TEXT NOT NULL,
    port          INTEGER NOT NULL UNIQUE,
    local_user    TEXT NOT NULL,
    public_key    TEXT NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen     DATETIME
);

CREATE TABLE IF NOT EXISTS access_keys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_name  TEXT NOT NULL REFERENCES machines(name) ON DELETE CASCADE,
    label         TEXT NOT NULL,
    public_key    TEXT NOT NULL,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(machine_name, public_key)
);
`

func migrate(db *DB) error {
	// Enable foreign keys
	if _, err := db.conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	_, err := db.conn.Exec(schema)
	return err
}
