package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/LipJ01/fly-ssh-bastion/internal/tunnel"
)

type clientConfig struct {
	ServerURL    string `json:"server_url"`
	APIKey       string `json:"api_key"`
	MachineName  string `json:"machine_name"`
	AssignedPort int    `json:"assigned_port,omitempty"`
	KeyPath      string `json:"key_path"`
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bastion")
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

func loadConfig() (*clientConfig, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return nil, fmt.Errorf("config not found - run 'bastion init' first")
	}
	var cfg clientConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

func saveConfig(cfg *clientConfig) error {
	if err := os.MkdirAll(configDir(), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0600)
}

func apiRequest(cfg *clientConfig, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	if !strings.HasPrefix(cfg.ServerURL, "https://") {
		return nil, fmt.Errorf("server URL must use HTTPS")
	}
	url := strings.TrimRight(cfg.ServerURL, "/") + path
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	return client.Do(req)
}

func main() {
	root := &cobra.Command{
		Use:   "bastion",
		Short: "SSH bastion tunnel manager",
	}

	root.AddCommand(initCmd())
	root.AddCommand(registerCmd())
	root.AddCommand(connectCmd())
	root.AddCommand(installCmd())
	root.AddCommand(uninstallCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(listCmd())
	root.AddCommand(deleteCmd())
	root.AddCommand(renameCmd())
	root.AddCommand(configCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive setup: configure server URL, API key, and generate SSH keypair",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := &clientConfig{}

			// Check for existing config
			if existing, err := loadConfig(); err == nil {
				cfg = existing
				fmt.Println("Existing config found. Press Enter to keep current values.")
			}

			fmt.Printf("Server URL [%s]: ", defaultStr(cfg.ServerURL, ""))
			var input string
			fmt.Scanln(&input)
			if input != "" {
				cfg.ServerURL = input
			}
			if cfg.ServerURL == "" {
				return fmt.Errorf("server URL is required")
			}

			fmt.Printf("API Key [%s]: ", maskStr(cfg.APIKey))
			input = ""
			fmt.Scanln(&input)
			if input != "" {
				cfg.APIKey = input
			}

			hostname, _ := os.Hostname()
			fmt.Printf("Machine name [%s]: ", defaultStr(cfg.MachineName, hostname))
			input = ""
			fmt.Scanln(&input)
			if input != "" {
				cfg.MachineName = input
			} else if cfg.MachineName == "" {
				cfg.MachineName = hostname
			}

			// SSH key generation
			home, _ := os.UserHomeDir()
			defaultKeyPath := filepath.Join(home, ".ssh", "bastion-key")
			fmt.Printf("SSH key path [%s]: ", defaultStr(cfg.KeyPath, defaultKeyPath))
			input = ""
			fmt.Scanln(&input)
			if input != "" {
				cfg.KeyPath = input
			} else if cfg.KeyPath == "" {
				cfg.KeyPath = defaultKeyPath
			}

			// Expand ~ in path
			if strings.HasPrefix(cfg.KeyPath, "~/") {
				cfg.KeyPath = filepath.Join(home, cfg.KeyPath[2:])
			}

			if _, err := os.Stat(cfg.KeyPath); os.IsNotExist(err) {
				fmt.Println("Generating SSH keypair...")
				if err := os.MkdirAll(filepath.Dir(cfg.KeyPath), 0700); err != nil {
					return err
				}
				genCmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", cfg.KeyPath, "-N", "", "-C", "bastion-"+cfg.MachineName)
				genCmd.Stdout = os.Stdout
				genCmd.Stderr = os.Stderr
				if err := genCmd.Run(); err != nil {
					return fmt.Errorf("failed to generate key: %w", err)
				}
			} else {
				fmt.Println("SSH key already exists, keeping it.")
			}

			if err := saveConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("\nConfig saved to %s\n", configPath())
			fmt.Println("Next: run 'bastion register' to register this machine with the server.")
			return nil
		},
	}
}

func registerCmd() *cobra.Command {
	var owner string
	var localUser string

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register this machine with the bastion server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			// Read public key
			pubKeyPath := cfg.KeyPath + ".pub"
			pubKeyData, err := os.ReadFile(pubKeyPath)
			if err != nil {
				return fmt.Errorf("cannot read public key %s: %w", pubKeyPath, err)
			}

			if localUser == "" {
				localUser = os.Getenv("USER")
			}

			body := map[string]string{
				"name":       cfg.MachineName,
				"owner":      owner,
				"local_user": localUser,
				"public_key": strings.TrimSpace(string(pubKeyData)),
			}

			resp, err := apiRequest(cfg, "POST", "/api/register", body)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close()

			respBody, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusCreated {
				return fmt.Errorf("registration failed (%d): %s", resp.StatusCode, string(respBody))
			}

			var result struct {
				Name            string `json:"name"`
				Port            int    `json:"port"`
				Server          string `json:"server"`
				TunnelPort      int    `json:"tunnel_port"`
				SSHUser         string `json:"ssh_user"`
				ServerPublicKey string `json:"server_public_key"`
			}
			json.Unmarshal(respBody, &result)

			cfg.AssignedPort = result.Port
			if err := saveConfig(cfg); err != nil {
				return err
			}

			// Add server public key to local authorized_keys so sshpiper can
			// authenticate through the reverse tunnel to this machine's sshd
			if result.ServerPublicKey != "" {
				if err := addToAuthorizedKeys(result.ServerPublicKey); err != nil {
					fmt.Printf("Warning: failed to add server key to authorized_keys: %v\n", err)
					fmt.Printf("You may need to manually add this key:\n  %s\n", result.ServerPublicKey)
				} else {
					fmt.Println("Added server public key to ~/.ssh/authorized_keys")
				}
			}

			fmt.Printf("Registered successfully!\n")
			fmt.Printf("  Machine: %s\n", result.Name)
			fmt.Printf("  Port:    %d\n", result.Port)
			fmt.Printf("  Server:  %s\n", result.Server)

			// Auto-install launchd service on macOS
			if runtime.GOOS == "darwin" {
				fmt.Println("\nInstalling tunnel service...")
				if err := installService(); err != nil {
					fmt.Printf("Warning: failed to install service: %v\n", err)
					fmt.Println("You can install it manually with: bastion install")
				} else {
					fmt.Println("Tunnel service installed and running.")
				}
			} else {
				fmt.Println("\nRun 'bastion connect' to start the tunnel.")
			}

			// Print SSH client instructions
			serverHost := strings.TrimPrefix(cfg.ServerURL, "https://")
			serverHost = strings.TrimPrefix(serverHost, "http://")
			serverHost = strings.TrimRight(serverHost, "/")

			fmt.Printf("\n--- SSH client setup ---\n")
			fmt.Printf("To connect to this machine from any SSH client:\n\n")
			fmt.Printf("  ssh -i <your-bastion-key> %s@%s\n\n", result.Name, serverHost)
			fmt.Printf("Or add to ~/.ssh/config on your other devices:\n\n")
			fmt.Printf("  Host %s\n", result.Name)
			fmt.Printf("      HostName %s\n", serverHost)
			fmt.Printf("      User %s\n", result.Name)
			fmt.Printf("      IdentityFile ~/.ssh/bastion-key\n\n")
			fmt.Printf("For iOS (Blink Shell):\n")
			fmt.Printf("  Host: %s | Port: 22 | User: %s | Key: bastion-key\n", serverHost, result.Name)
			return nil
		},
	}

	cmd.Flags().StringVar(&owner, "owner", "", "Owner name (required)")
	cmd.Flags().StringVar(&localUser, "local-user", "", "Local SSH username (defaults to $USER)")
	cmd.MarkFlagRequired("owner")
	return cmd
}

func connectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect",
		Short: "Establish reverse SSH tunnel (foreground, with auto-reconnect)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.AssignedPort == 0 {
				return fmt.Errorf("no assigned port - run 'bastion register' first")
			}

			// Parse server host from URL
			serverHost := strings.TrimPrefix(cfg.ServerURL, "https://")
			serverHost = strings.TrimPrefix(serverHost, "http://")
			serverHost = strings.TrimRight(serverHost, "/")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Handle signals
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sig
				fmt.Println("\nDisconnecting...")
				cancel()
			}()

			// Start heartbeat in background
			go heartbeatLoop(ctx, cfg)

			fmt.Printf("Connecting tunnel: localhost:22 -> %s:%d (remote port %d)\n",
				serverHost, 2222, cfg.AssignedPort)

			return tunnel.Run(ctx, tunnel.Config{
				ServerHost: serverHost,
				TunnelPort: 2222,
				LocalPort:  22,
				RemotePort: cfg.AssignedPort,
				KeyPath:    cfg.KeyPath,
				SSHUser:    "bastion",
			})
		},
	}
}

func heartbeatLoop(ctx context.Context, cfg *clientConfig) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			body := map[string]string{"name": cfg.MachineName}
			resp, err := apiRequest(cfg, "POST", "/api/heartbeat", body)
			if err != nil {
				log.Printf("Heartbeat failed: %v", err)
				continue
			}
			resp.Body.Close()
		}
	}
}

func installService() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.AssignedPort == 0 {
		return fmt.Errorf("no assigned port - run 'bastion register' first")
	}

	bastionPath, err := os.Executable()
	if err != nil {
		bastionPath, err = exec.LookPath("bastion")
		if err != nil {
			return fmt.Errorf("cannot find bastion binary path")
		}
	}

	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.bastion.tunnel.plist")
	logPath := filepath.Join(home, "Library", "Logs", "bastion-tunnel.log")

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.bastion.tunnel</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>connect</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
    <key>ThrottleInterval</key>
    <integer>10</integer>
</dict>
</plist>`, bastionPath, logPath, logPath)

	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return err
	}

	exec.Command("launchctl", "unload", plistPath).Run()

	loadCmd := exec.Command("launchctl", "load", plistPath)
	loadCmd.Stdout = os.Stdout
	loadCmd.Stderr = os.Stderr
	if err := loadCmd.Run(); err != nil {
		return fmt.Errorf("failed to load plist: %w", err)
	}

	fmt.Printf("  Plist: %s\n", plistPath)
	fmt.Printf("  Logs:  %s\n", logPath)
	return nil
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install launchd plist for persistent tunnel (macOS)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				return fmt.Errorf("install is only supported on macOS")
			}
			return installService()
		},
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove launchd plist",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				return fmt.Errorf("uninstall is only supported on macOS")
			}

			home, _ := os.UserHomeDir()
			plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.bastion.tunnel.plist")

			exec.Command("launchctl", "unload", plistPath).Run()

			if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
				return err
			}

			fmt.Println("Uninstalled launchd plist.")
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show tunnel status and server health",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			fmt.Printf("Machine: %s\n", cfg.MachineName)
			fmt.Printf("Port:    %d\n", cfg.AssignedPort)
			fmt.Printf("Key:     %s\n", cfg.KeyPath)

			// Check launchd status (macOS)
			if runtime.GOOS == "darwin" {
				out, err := exec.Command("launchctl", "list", "com.bastion.tunnel").Output()
				if err != nil {
					fmt.Println("Tunnel:  not installed (launchd)")
				} else {
					fmt.Printf("Tunnel:  installed (launchd)\n%s", string(out))
				}
			}

			// Query server
			fmt.Println("\nServer status:")
			resp, err := apiRequest(cfg, "GET", "/api/status", nil)
			if err != nil {
				fmt.Printf("  Error: %v\n", err)
				return nil
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			var status map[string]any
			json.Unmarshal(body, &status)
			for k, v := range status {
				fmt.Printf("  %s: %v\n", k, v)
			}
			return nil
		},
	}
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all registered machines",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			resp, err := apiRequest(cfg, "GET", "/api/machines", nil)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("failed (%d): %s", resp.StatusCode, string(body))
			}

			var machines []struct {
				Name      string  `json:"name"`
				Owner     string  `json:"owner"`
				Port      int     `json:"port"`
				LocalUser string  `json:"local_user"`
				LastSeen  *string `json:"last_seen,omitempty"`
			}
			json.NewDecoder(resp.Body).Decode(&machines)

			if len(machines) == 0 {
				fmt.Println("No machines registered.")
				return nil
			}

			fmt.Printf("%-20s %-10s %-6s %-15s %s\n", "NAME", "OWNER", "PORT", "USER", "LAST SEEN")
			for _, m := range machines {
				lastSeen := "never"
				if m.LastSeen != nil {
					lastSeen = *m.LastSeen
				}
				fmt.Printf("%-20s %-10s %-6d %-15s %s\n", m.Name, m.Owner, m.Port, m.LocalUser, lastSeen)
			}
			return nil
		},
	}
}

func deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a machine from the server (defaults to this machine)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			name := cfg.MachineName
			if len(args) > 0 {
				name = args[0]
			}

			resp, err := apiRequest(cfg, "DELETE", "/api/machines/"+name, nil)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("delete failed (%d): %s", resp.StatusCode, string(body))
			}

			fmt.Printf("Deleted machine %q\n", name)

			// If deleting self, clean up local state
			if name == cfg.MachineName {
				if runtime.GOOS == "darwin" {
					home, _ := os.UserHomeDir()
					plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.bastion.tunnel.plist")
					exec.Command("launchctl", "unload", plistPath).Run()
					os.Remove(plistPath)
					fmt.Println("Uninstalled launchd service.")
				}
				cfg.AssignedPort = 0
				if err := saveConfig(cfg); err != nil {
					return fmt.Errorf("failed to update config: %w", err)
				}
				fmt.Println("Cleared assigned port from local config.")
			}

			return nil
		},
	}
}

func renameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <new-name>",
		Short: "Rename this machine on the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			newName := args[0]
			body := map[string]string{"new_name": newName}

			resp, err := apiRequest(cfg, "PUT", "/api/machines/"+cfg.MachineName+"/rename", body)
			if err != nil {
				return fmt.Errorf("request failed: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				respBody, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("rename failed (%d): %s", resp.StatusCode, string(respBody))
			}

			oldName := cfg.MachineName
			cfg.MachineName = newName
			if err := saveConfig(cfg); err != nil {
				return fmt.Errorf("failed to update config: %w", err)
			}

			fmt.Printf("Renamed %q -> %q\n", oldName, newName)
			return nil
		},
	}
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or update client configuration",
	}

	validKeys := map[string]bool{
		"server_url":   true,
		"api_key":      true,
		"machine_name": true,
		"key_path":     true,
	}
	readOnlyKeys := map[string]bool{
		"assigned_port": true,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "get <key>",
		Short: "Get a config value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !validKeys[key] && !readOnlyKeys[key] {
				return fmt.Errorf("unknown key %q (valid: server_url, api_key, machine_name, key_path, assigned_port)", key)
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			val := getConfigValue(cfg, key)
			if key == "api_key" {
				val = maskStr(val)
			}
			fmt.Println(val)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if !validKeys[key] {
				if readOnlyKeys[key] {
					return fmt.Errorf("%q is read-only (set by server during register)", key)
				}
				return fmt.Errorf("unknown key %q (valid: server_url, api_key, machine_name, key_path)", key)
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			setConfigValue(cfg, key, value)
			if err := saveConfig(cfg); err != nil {
				return err
			}
			fmt.Printf("Set %s = %s\n", key, value)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all config values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			fmt.Printf("%-15s %s\n", "server_url", cfg.ServerURL)
			fmt.Printf("%-15s %s\n", "api_key", maskStr(cfg.APIKey))
			fmt.Printf("%-15s %s\n", "machine_name", cfg.MachineName)
			fmt.Printf("%-15s %s\n", "key_path", cfg.KeyPath)
			fmt.Printf("%-15s %d\n", "assigned_port", cfg.AssignedPort)
			return nil
		},
	})

	return cmd
}

func getConfigValue(cfg *clientConfig, key string) string {
	switch key {
	case "server_url":
		return cfg.ServerURL
	case "api_key":
		return cfg.APIKey
	case "machine_name":
		return cfg.MachineName
	case "key_path":
		return cfg.KeyPath
	case "assigned_port":
		return fmt.Sprintf("%d", cfg.AssignedPort)
	default:
		return ""
	}
}

func setConfigValue(cfg *clientConfig, key, value string) {
	switch key {
	case "server_url":
		cfg.ServerURL = value
	case "api_key":
		cfg.APIKey = value
	case "machine_name":
		cfg.MachineName = value
	case "key_path":
		cfg.KeyPath = value
	}
}

func addToAuthorizedKeys(pubKey string) error {
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}
	authKeysPath := filepath.Join(sshDir, "authorized_keys")

	// Check if key is already present
	existing, _ := os.ReadFile(authKeysPath)
	if strings.Contains(string(existing), pubKey) {
		return nil
	}

	f, err := os.OpenFile(authKeysPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Ensure we start on a new line
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		f.WriteString("\n")
	}
	_, err = f.WriteString(pubKey + "\n")
	return err
}

func defaultStr(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func maskStr(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "****"
}
