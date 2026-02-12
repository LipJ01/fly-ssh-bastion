package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	PortMin = 10022
	PortMax = 10099
)

type Machine struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Owner     string    `json:"owner"`
	Port      int       `json:"port"`
	LocalUser string    `json:"local_user"`
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db := &DB{conn: conn}
	if err := migrate(db); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) AllocatePort() (int, error) {
	used := make(map[int]bool)
	rows, err := db.conn.Query("SELECT port FROM machines")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return 0, err
		}
		used[port] = true
	}
	for p := PortMin; p <= PortMax; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no available ports (all %d slots in use)", PortMax-PortMin+1)
}

func (db *DB) CreateMachine(m *Machine) error {
	port, err := db.AllocatePort()
	if err != nil {
		return err
	}
	m.Port = port
	result, err := db.conn.Exec(
		"INSERT INTO machines (name, owner, port, local_user, public_key) VALUES (?, ?, ?, ?, ?)",
		m.Name, m.Owner, m.Port, m.LocalUser, m.PublicKey,
	)
	if err != nil {
		return fmt.Errorf("insert machine: %w", err)
	}
	m.ID, _ = result.LastInsertId()
	return nil
}

func (db *DB) GetMachine(name string) (*Machine, error) {
	m := &Machine{}
	err := db.conn.QueryRow(
		"SELECT id, name, owner, port, local_user, public_key, created_at, last_seen FROM machines WHERE name = ?",
		name,
	).Scan(&m.ID, &m.Name, &m.Owner, &m.Port, &m.LocalUser, &m.PublicKey, &m.CreatedAt, &m.LastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (db *DB) ListMachines() ([]Machine, error) {
	rows, err := db.conn.Query(
		"SELECT id, name, owner, port, local_user, public_key, created_at, last_seen FROM machines ORDER BY port",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var machines []Machine
	for rows.Next() {
		var m Machine
		if err := rows.Scan(&m.ID, &m.Name, &m.Owner, &m.Port, &m.LocalUser, &m.PublicKey, &m.CreatedAt, &m.LastSeen); err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}
	return machines, nil
}

func (db *DB) DeleteMachine(name string) error {
	result, err := db.conn.Exec("DELETE FROM machines WHERE name = ?", name)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("machine %q not found", name)
	}
	return nil
}

func (db *DB) RenameMachine(oldName, newName string) error {
	result, err := db.conn.Exec("UPDATE machines SET name = ? WHERE name = ?", newName, oldName)
	if err != nil {
		return fmt.Errorf("rename machine: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("machine %q not found", oldName)
	}
	return nil
}

func (db *DB) UpdateLastSeen(name string) error {
	result, err := db.conn.Exec("UPDATE machines SET last_seen = CURRENT_TIMESTAMP WHERE name = ?", name)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("machine %q not found", name)
	}
	return nil
}
