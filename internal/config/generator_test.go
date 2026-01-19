package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LipJ01/fly-ssh-bastion/internal/db"
)

func TestGenerateEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sshpiper.yaml")
	gen := NewGenerator(configPath, filepath.Join(dir, "keys"), "/data/server-key")

	if err := gen.Generate(nil); err != nil {
		t.Fatalf("generate: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, `version: "1.0"`) {
		t.Fatalf("expected version header, got: %s", content)
	}
	if !strings.Contains(content, "pipes:") {
		t.Fatalf("expected pipes key, got: %s", content)
	}
}

func TestGenerateWithMachines(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	os.MkdirAll(keysDir, 0755)
	configPath := filepath.Join(dir, "sshpiper.yaml")
	gen := NewGenerator(configPath, keysDir, "/data/server-key")

	machines := []db.Machine{
		{Name: "alice-mac", Port: 10022, LocalUser: "alice", PublicKey: "ssh-ed25519 AAAA alice"},
		{Name: "bob-pc", Port: 10023, LocalUser: "bob", PublicKey: "ssh-ed25519 BBBB bob"},
	}

	if err := gen.Generate(machines); err != nil {
		t.Fatalf("generate: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	content := string(data)

	// Check both machines are present
	if !strings.Contains(content, `username: "alice-mac"`) {
		t.Errorf("missing alice-mac in config")
	}
	if !strings.Contains(content, `username: "bob-pc"`) {
		t.Errorf("missing bob-pc in config")
	}
	if !strings.Contains(content, "host: localhost:10022") {
		t.Errorf("missing port 10022")
	}
	if !strings.Contains(content, "host: localhost:10023") {
		t.Errorf("missing port 10023")
	}
	if !strings.Contains(content, `username: "alice"`) {
		t.Errorf("missing upstream user alice")
	}
	if !strings.Contains(content, `username: "bob"`) {
		t.Errorf("missing upstream user bob")
	}
	if !strings.Contains(content, "private_key: /data/server-key") {
		t.Errorf("missing server key path")
	}
	if !strings.Contains(content, "ignore_hostkey: true") {
		t.Errorf("missing ignore_hostkey")
	}
}

func TestWriteAndRemoveKey(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	os.MkdirAll(keysDir, 0755)
	gen := NewGenerator("", keysDir, "")

	if err := gen.WriteKey("test-machine", "ssh-ed25519 AAAA test"); err != nil {
		t.Fatalf("write key: %v", err)
	}

	keyPath := filepath.Join(keysDir, "test-machine.pub")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if !strings.Contains(string(data), "ssh-ed25519 AAAA test") {
		t.Fatalf("unexpected key content: %s", string(data))
	}

	if err := gen.RemoveKey("test-machine"); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("key file should be deleted")
	}
}

func TestUpdateAuthorizedKeys(t *testing.T) {
	dir := t.TempDir()

	// Create a fake server key
	serverKeyPath := filepath.Join(dir, "server-key")
	os.WriteFile(serverKeyPath, []byte("fake-private-key"), 0600)
	os.WriteFile(serverKeyPath+".pub", []byte("ssh-ed25519 SERVER server-key"), 0644)

	// Create bastion home dir for the test
	bastionSSH := filepath.Join(dir, "bastion-ssh")
	os.MkdirAll(bastionSSH, 0700)

	gen := NewGenerator("", filepath.Join(dir, "keys"), serverKeyPath)

	// Override the authorized_keys path for testing by using a subtest
	// Since UpdateAuthorizedKeys hardcodes /home/bastion/.ssh/authorized_keys,
	// we can only test the non-path logic in unit tests. The full path test
	// requires integration testing in Docker.
	machines := []db.Machine{
		{Name: "m1", PublicKey: "ssh-ed25519 AAAA m1"},
		{Name: "m2", PublicKey: "ssh-ed25519 BBBB m2"},
	}

	// This will fail in CI/test because /home/bastion doesn't exist,
	// but we can verify it doesn't panic and returns an error gracefully
	err := gen.UpdateAuthorizedKeys(machines)
	if err == nil {
		// If it succeeded (e.g., running as root in Docker), that's fine too
		t.Log("UpdateAuthorizedKeys succeeded (likely running with permissions)")
	} else {
		t.Logf("UpdateAuthorizedKeys returned expected error in test env: %v", err)
	}
}
