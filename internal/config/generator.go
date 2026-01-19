package config

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/LipJ01/fly-ssh-bastion/internal/db"
)

const sshpiperTemplate = `version: "1.0"
pipes:
{{- range .Machines }}
  - from:
      - username: "{{ .Name }}"
        authorized_keys:
          - {{ $.KeysDir }}/{{ .Name }}.pub
    to:
      host: localhost:{{ .Port }}
      username: "{{ .LocalUser }}"
      private_key: {{ $.ServerKey }}
      ignore_hostkey: true
{{- end }}
`

type templateData struct {
	Machines  []db.Machine
	KeysDir   string
	ServerKey string
}

type Generator struct {
	ConfigPath string
	KeysDir    string
	ServerKey  string
}

func NewGenerator(configPath, keysDir, serverKey string) *Generator {
	return &Generator{
		ConfigPath: configPath,
		KeysDir:    keysDir,
		ServerKey:  serverKey,
	}
}

// WriteKey writes a machine's public key to the keys directory.
func (g *Generator) WriteKey(name, publicKey string) error {
	path := filepath.Join(g.KeysDir, name+".pub")
	return os.WriteFile(path, []byte(publicKey+"\n"), 0644)
}

// RemoveKey removes a machine's public key file.
func (g *Generator) RemoveKey(name string) error {
	path := filepath.Join(g.KeysDir, name+".pub")
	return os.Remove(path)
}

// UpdateAuthorizedKeys rebuilds /home/bastion/.ssh/authorized_keys with all
// machine public keys plus the server key, so machines can establish reverse tunnels.
func (g *Generator) UpdateAuthorizedKeys(machines []db.Machine) error {
	authKeysPath := "/home/bastion/.ssh/authorized_keys"

	// Start with server public key
	var keys []byte
	serverPubKey, err := os.ReadFile(g.ServerKey + ".pub")
	if err == nil {
		keys = append(keys, serverPubKey...)
		if len(keys) > 0 && keys[len(keys)-1] != '\n' {
			keys = append(keys, '\n')
		}
	}

	// Add machine keys with port restrictions
	for _, m := range machines {
		entry := fmt.Sprintf("permitlisten=\"localhost:%d\",no-pty,no-agent-forwarding,no-X11-forwarding %s\n", m.Port, m.PublicKey)
		keys = append(keys, []byte(entry)...)
	}

	if err := os.MkdirAll(filepath.Dir(authKeysPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(authKeysPath, keys, 0600)
}

// Generate writes the sshpiper.yaml config from the current machine list.
func (g *Generator) Generate(machines []db.Machine) error {
	tmpl, err := template.New("sshpiper").Parse(sshpiperTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	f, err := os.Create(g.ConfigPath)
	if err != nil {
		return fmt.Errorf("create config: %w", err)
	}
	defer f.Close()

	data := templateData{
		Machines:  machines,
		KeysDir:   g.KeysDir,
		ServerKey: g.ServerKey,
	}
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	return nil
}
