package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/LipJ01/fly-ssh-bastion/internal/config"
	"github.com/LipJ01/fly-ssh-bastion/internal/db"
	"github.com/LipJ01/fly-ssh-bastion/internal/server"
)

var (
	dbPath     = flag.String("db", "/data/db/bastion.db", "SQLite database path")
	keysDir    = flag.String("keys-dir", "/data/keys", "Directory for machine public keys")
	configPath = flag.String("config-path", "/data/sshpiper.yaml", "Path to write sshpiper.yaml")
	serverKey  = flag.String("server-key", "/data/server-key", "Path to server SSH private key")
	listen     = flag.String("listen", ":8080", "HTTP listen address")
)

func main() {
	flag.Parse()

	apiSecret := os.Getenv("API_SECRET_KEY")
	if apiSecret == "" {
		log.Fatal("API_SECRET_KEY environment variable is required")
	}
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		log.Fatal("SERVER_URL environment variable is required")
	}

	// Open database
	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Config generator
	gen := config.NewGenerator(*configPath, *keysDir, *serverKey)

	// Generate initial config from DB state
	machines, err := database.ListMachines()
	if err != nil {
		log.Fatalf("Failed to list machines: %v", err)
	}
	// Write all existing keys
	for _, m := range machines {
		if err := gen.WriteKey(m.Name, m.PublicKey); err != nil {
			log.Printf("Warning: failed to write key for %s: %v", m.Name, err)
		}
	}
	if err := gen.Generate(machines); err != nil {
		log.Fatalf("Failed to generate initial config: %v", err)
	}
	if err := gen.UpdateAuthorizedKeys(machines); err != nil {
		log.Printf("Warning: failed to update authorized_keys: %v", err)
	}
	log.Printf("Generated sshpiper config for %d machines", len(machines))

	// Start sshd
	sshd := startProcess("sshd", "/usr/sbin/sshd", "-D", "-e")

	// Give sshd time to start
	time.Sleep(time.Second)

	// Start sshpiperd
	sshpiper := startProcess("sshpiperd",
		"/usr/local/bin/sshpiperd",
		"-p", "2223",
		"-i", "/etc/sshpiper/ssh_host_ed25519_key",
		"--log-level", "info",
		"yaml", "--config", *configPath, "--no-check-perm",
	)

	// Reload function: restart sshpiperd to pick up new config
	reloadConfig := func() {
		log.Println("Config changed, restarting sshpiperd...")
		if sshpiper.Process != nil {
			sshpiper.Process.Signal(syscall.SIGTERM)
			sshpiper.Wait()
		}
		sshpiper = startProcess("sshpiperd",
			"/usr/local/bin/sshpiperd",
			"-p", "2223",
			"-i", "/etc/sshpiper/ssh_host_ed25519_key",
			"--log-level", "info",
			"yaml", "--config", *configPath, "--no-check-perm",
		)
	}

	// HTTP API
	router := server.NewRouter(database, gen, apiSecret, serverURL, reloadConfig)

	httpServer := &http.Server{
		Addr:    *listen,
		Handler: router,
	}

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("Shutting down...")
		httpServer.Close()
		if sshpiper.Process != nil {
			sshpiper.Process.Signal(syscall.SIGTERM)
		}
		if sshd.Process != nil {
			sshd.Process.Signal(syscall.SIGTERM)
		}
		os.Exit(0)
	}()

	log.Printf("API server listening on %s", *listen)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}

func startProcess(name string, path string, args ...string) *exec.Cmd {
	cmd := exec.Command(path, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start %s: %v", name, err)
	}
	log.Printf("Started %s (pid %d)", name, cmd.Process.Pid)

	// Monitor in background
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("%s exited: %v", name, err)
		}
	}()

	return cmd
}
