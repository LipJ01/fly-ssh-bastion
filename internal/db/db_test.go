package db

import (
	"os"
	"path/filepath"
	"testing"
)

func tempDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateAndGetMachine(t *testing.T) {
	db := tempDB(t)

	m := &Machine{
		Name:      "test-machine",
		Owner:     "alice",
		LocalUser: "alice",
		PublicKey: "ssh-ed25519 AAAA... test",
	}
	if err := db.CreateMachine(m); err != nil {
		t.Fatalf("create: %v", err)
	}

	if m.Port < PortMin || m.Port > PortMax {
		t.Fatalf("port %d out of range [%d, %d]", m.Port, PortMin, PortMax)
	}
	if m.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := db.GetMachine("test-machine")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected machine, got nil")
	}
	if got.Name != "test-machine" || got.Owner != "alice" || got.Port != m.Port {
		t.Fatalf("unexpected machine: %+v", got)
	}
}

func TestGetMachineNotFound(t *testing.T) {
	db := tempDB(t)

	got, err := db.GetMachine("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestPortAllocation(t *testing.T) {
	db := tempDB(t)

	// First machine gets PortMin
	m1 := &Machine{Name: "m1", Owner: "a", LocalUser: "a", PublicKey: "key1"}
	if err := db.CreateMachine(m1); err != nil {
		t.Fatalf("create m1: %v", err)
	}
	if m1.Port != PortMin {
		t.Fatalf("expected port %d, got %d", PortMin, m1.Port)
	}

	// Second machine gets PortMin+1
	m2 := &Machine{Name: "m2", Owner: "b", LocalUser: "b", PublicKey: "key2"}
	if err := db.CreateMachine(m2); err != nil {
		t.Fatalf("create m2: %v", err)
	}
	if m2.Port != PortMin+1 {
		t.Fatalf("expected port %d, got %d", PortMin+1, m2.Port)
	}
}

func TestPortReuse(t *testing.T) {
	db := tempDB(t)

	m1 := &Machine{Name: "m1", Owner: "a", LocalUser: "a", PublicKey: "key1"}
	db.CreateMachine(m1)
	m2 := &Machine{Name: "m2", Owner: "b", LocalUser: "b", PublicKey: "key2"}
	db.CreateMachine(m2)

	// Delete m1, port should be reused
	if err := db.DeleteMachine("m1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	m3 := &Machine{Name: "m3", Owner: "c", LocalUser: "c", PublicKey: "key3"}
	if err := db.CreateMachine(m3); err != nil {
		t.Fatalf("create m3: %v", err)
	}
	if m3.Port != PortMin {
		t.Fatalf("expected reused port %d, got %d", PortMin, m3.Port)
	}
}

func TestDuplicateName(t *testing.T) {
	db := tempDB(t)

	m1 := &Machine{Name: "same", Owner: "a", LocalUser: "a", PublicKey: "key1"}
	if err := db.CreateMachine(m1); err != nil {
		t.Fatalf("create: %v", err)
	}

	m2 := &Machine{Name: "same", Owner: "b", LocalUser: "b", PublicKey: "key2"}
	if err := db.CreateMachine(m2); err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestListMachines(t *testing.T) {
	db := tempDB(t)

	// Empty list
	machines, err := db.ListMachines()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(machines) != 0 {
		t.Fatalf("expected 0, got %d", len(machines))
	}

	db.CreateMachine(&Machine{Name: "a", Owner: "x", LocalUser: "x", PublicKey: "k1"})
	db.CreateMachine(&Machine{Name: "b", Owner: "y", LocalUser: "y", PublicKey: "k2"})

	machines, err = db.ListMachines()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(machines) != 2 {
		t.Fatalf("expected 2, got %d", len(machines))
	}
	// Should be ordered by port
	if machines[0].Port > machines[1].Port {
		t.Fatal("expected machines ordered by port")
	}
}

func TestDeleteMachineNotFound(t *testing.T) {
	db := tempDB(t)

	if err := db.DeleteMachine("ghost"); err == nil {
		t.Fatal("expected error deleting nonexistent machine")
	}
}

func TestUpdateLastSeen(t *testing.T) {
	db := tempDB(t)

	db.CreateMachine(&Machine{Name: "m1", Owner: "a", LocalUser: "a", PublicKey: "k"})

	if err := db.UpdateLastSeen("m1"); err != nil {
		t.Fatalf("update: %v", err)
	}

	m, _ := db.GetMachine("m1")
	if m.LastSeen == nil {
		t.Fatal("expected last_seen to be set")
	}
}

func TestUpdateLastSeenNotFound(t *testing.T) {
	db := tempDB(t)

	if err := db.UpdateLastSeen("ghost"); err == nil {
		t.Fatal("expected error for nonexistent machine")
	}
}

func TestRenameMachine(t *testing.T) {
	db := tempDB(t)

	db.CreateMachine(&Machine{Name: "old-name", Owner: "a", LocalUser: "a", PublicKey: "k"})

	if err := db.RenameMachine("old-name", "new-name"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Old name should be gone
	got, _ := db.GetMachine("old-name")
	if got != nil {
		t.Fatal("expected old name to be gone")
	}

	// New name should exist
	got, _ = db.GetMachine("new-name")
	if got == nil {
		t.Fatal("expected new name to exist")
	}
	if got.Owner != "a" {
		t.Fatalf("expected owner to be preserved, got %s", got.Owner)
	}
}

func TestRenameMachineNotFound(t *testing.T) {
	db := tempDB(t)

	if err := db.RenameMachine("ghost", "new-name"); err == nil {
		t.Fatal("expected error renaming nonexistent machine")
	}
}

func TestRenameMachineDuplicate(t *testing.T) {
	db := tempDB(t)

	db.CreateMachine(&Machine{Name: "m1", Owner: "a", LocalUser: "a", PublicKey: "k1"})
	db.CreateMachine(&Machine{Name: "m2", Owner: "b", LocalUser: "b", PublicKey: "k2"})

	if err := db.RenameMachine("m1", "m2"); err == nil {
		t.Fatal("expected error renaming to duplicate name")
	}
}

func TestPortExhaustion(t *testing.T) {
	db := tempDB(t)

	// Fill all ports
	for i := PortMin; i <= PortMax; i++ {
		m := &Machine{
			Name:      "m" + os.TempDir() + string(rune(i)),
			Owner:     "x",
			LocalUser: "x",
			PublicKey: "k",
		}
		m.Name = "m" + string(rune('A'+i-PortMin))
		if err := db.CreateMachine(m); err != nil {
			t.Fatalf("create machine %d: %v", i, err)
		}
	}

	// Next should fail
	extra := &Machine{Name: "overflow", Owner: "x", LocalUser: "x", PublicKey: "k"}
	if err := db.CreateMachine(extra); err == nil {
		t.Fatal("expected port exhaustion error")
	}
}
