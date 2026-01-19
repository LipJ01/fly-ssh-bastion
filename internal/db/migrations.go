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
`

func migrate(db *DB) error {
	_, err := db.conn.Exec(schema)
	return err
}
