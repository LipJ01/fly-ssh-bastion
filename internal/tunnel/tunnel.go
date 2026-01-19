package tunnel

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Config struct {
	ServerHost string
	TunnelPort int // remote sshd port (2222)
	LocalPort  int // local SSH port to forward (22)
	RemotePort int // assigned remote port (e.g. 10024)
	KeyPath    string
	SSHUser    string
}

// Run starts the reverse SSH tunnel with automatic reconnection.
// It blocks until the context is cancelled.
func Run(ctx context.Context, cfg Config) error {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Printf("Connecting tunnel (attempt %d)...", attempt+1)
		err := runOnce(ctx, cfg)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		attempt++
		delay := backoff(attempt)
		log.Printf("Tunnel disconnected: %v. Reconnecting in %s...", err, delay)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func runOnce(ctx context.Context, cfg Config) error {
	knownHostsPath := filepath.Join(filepath.Dir(cfg.KeyPath), "bastion_known_hosts")
	args := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", fmt.Sprintf("UserKnownHostsFile=%s", knownHostsPath),
		"-i", cfg.KeyPath,
		"-R", fmt.Sprintf("%d:localhost:%d", cfg.RemotePort, cfg.LocalPort),
		"-p", fmt.Sprintf("%d", cfg.TunnelPort),
		fmt.Sprintf("%s@%s", cfg.SSHUser, cfg.ServerHost),
	}

	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return err
	}
	return fmt.Errorf("ssh exited cleanly")
}

func backoff(attempt int) time.Duration {
	// Exponential backoff: 2s, 4s, 8s, 16s, 32s, capped at 60s
	secs := math.Pow(2, float64(attempt))
	if secs > 60 {
		secs = 60
	}
	return time.Duration(secs) * time.Second
}
