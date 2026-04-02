package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LipJ01/fly-ssh-bastion/internal/config"
	"github.com/LipJ01/fly-ssh-bastion/internal/db"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func validatePublicKey(key string) error {
	key = strings.TrimSpace(key)
	if strings.Contains(key, "\n") {
		return fmt.Errorf("public key must be a single line")
	}
	if len(key) > 2048 {
		return fmt.Errorf("public key too large")
	}
	parts := strings.Fields(key)
	if len(parts) < 2 {
		return fmt.Errorf("invalid SSH public key format")
	}
	validTypes := map[string]bool{
		"ssh-rsa": true, "ssh-ed25519": true,
		"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
		"ssh-dss": true,
	}
	if !validTypes[parts[0]] {
		return fmt.Errorf("unsupported key type: %s", parts[0])
	}
	return nil
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type Handlers struct {
	DB        *db.DB
	Gen       *config.Generator
	OnChange  func() // called after config regeneration (e.g. reload sshpiperd)
	ServerURL string
}

type registerRequest struct {
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	LocalUser string `json:"local_user"`
	PublicKey string `json:"public_key"`
}

type registerResponse struct {
	Name            string `json:"name"`
	Port            int    `json:"port"`
	Server          string `json:"server"`
	TunnelPort      int    `json:"tunnel_port"`
	SSHUser         string `json:"ssh_user"`
	ServerPublicKey string `json:"server_public_key"`
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Owner == "" || req.LocalUser == "" || req.PublicKey == "" {
		jsonError(w, "name, owner, local_user, and public_key are required", http.StatusBadRequest)
		return
	}
	if !validName.MatchString(req.Name) {
		jsonError(w, "invalid machine name: must be alphanumeric with optional dots, hyphens, underscores (max 64 chars)", http.StatusBadRequest)
		return
	}
	if !validName.MatchString(req.Owner) {
		jsonError(w, "invalid owner: must be alphanumeric with optional dots, hyphens, underscores (max 64 chars)", http.StatusBadRequest)
		return
	}
	if !validName.MatchString(req.LocalUser) {
		jsonError(w, "invalid local_user: must be alphanumeric with optional dots, hyphens, underscores (max 64 chars)", http.StatusBadRequest)
		return
	}
	if err := validatePublicKey(req.PublicKey); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if already exists
	existing, err := h.DB.GetMachine(req.Name)
	if err != nil {
		log.Printf("error checking machine: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		jsonError(w, "machine already registered", http.StatusConflict)
		return
	}

	m := &db.Machine{
		Name:      req.Name,
		Owner:     req.Owner,
		LocalUser: req.LocalUser,
		PublicKey:  req.PublicKey,
	}
	if err := h.DB.CreateMachine(m); err != nil {
		log.Printf("error creating machine: %v", err)
		jsonError(w, "failed to register machine", http.StatusInternalServerError)
		return
	}

	if err := h.Gen.WriteKey(m.Name, m.PublicKey); err != nil {
		log.Printf("error writing key: %v", err)
	}

	if err := h.regenerateConfig(); err != nil {
		log.Printf("error regenerating config: %v", err)
	}

	// Read server public key to include in response
	var serverPubKey string
	if pubKeyData, err := os.ReadFile(h.Gen.ServerKey + ".pub"); err == nil {
		serverPubKey = strings.TrimSpace(string(pubKeyData))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(registerResponse{
		Name:            m.Name,
		Port:            m.Port,
		Server:          h.ServerURL,
		TunnelPort:      2222,
		SSHUser:         "bastion",
		ServerPublicKey: serverPubKey,
	})
}

type machineListEntry struct {
	Name      string     `json:"name"`
	Owner     string     `json:"owner"`
	Port      int        `json:"port"`
	LocalUser string     `json:"local_user"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

func (h *Handlers) ListMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := h.DB.ListMachines()
	if err != nil {
		log.Printf("error listing machines: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	result := make([]machineListEntry, len(machines))
	for i, m := range machines {
		result[i] = machineListEntry{
			Name:      m.Name,
			Owner:     m.Owner,
			Port:      m.Port,
			LocalUser: m.LocalUser,
			LastSeen:  m.LastSeen,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handlers) DeleteMachine(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := h.DB.DeleteMachine(name); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	_ = h.Gen.RemoveKey(name)

	if err := h.regenerateConfig(); err != nil {
		log.Printf("error regenerating config: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) RenameMachine(w http.ResponseWriter, r *http.Request) {
	oldName := chi.URLParam(r, "name")

	var req struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.NewName == "" {
		jsonError(w, "new_name is required", http.StatusBadRequest)
		return
	}
	if !validName.MatchString(req.NewName) {
		jsonError(w, "invalid new name: must be alphanumeric with optional dots, hyphens, underscores (max 64 chars)", http.StatusBadRequest)
		return
	}

	// Check if new name already taken
	existing, err := h.DB.GetMachine(req.NewName)
	if err != nil {
		log.Printf("error checking machine: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		jsonError(w, fmt.Sprintf("machine %q already exists", req.NewName), http.StatusConflict)
		return
	}

	if err := h.DB.RenameMachine(oldName, req.NewName); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := h.Gen.RenameKey(oldName, req.NewName); err != nil {
		log.Printf("warning: failed to rename key file: %v", err)
	}

	if err := h.regenerateConfig(); err != nil {
		log.Printf("error regenerating config: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"name": req.NewName})
}

func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	machines, err := h.DB.ListMachines()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":        "ok",
		"machine_count": len(machines),
	})
}

func (h *Handlers) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := h.DB.UpdateLastSeen(req.Name); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) AddAccessKey(w http.ResponseWriter, r *http.Request) {
	machineName := chi.URLParam(r, "name")

	machine, err := h.DB.GetMachine(machineName)
	if err != nil {
		log.Printf("error getting machine: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if machine == nil {
		jsonError(w, "machine not found", http.StatusNotFound)
		return
	}

	var req struct {
		Label     string `json:"label"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Label == "" || req.PublicKey == "" {
		jsonError(w, "label and public_key are required", http.StatusBadRequest)
		return
	}
	if err := validatePublicKey(req.PublicKey); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.PublicKey = strings.TrimSpace(req.PublicKey)

	key, err := h.DB.AddAccessKey(machineName, req.Label, req.PublicKey)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			jsonError(w, "key already added to this machine", http.StatusConflict)
			return
		}
		log.Printf("error adding access key: %v", err)
		jsonError(w, "failed to add access key", http.StatusInternalServerError)
		return
	}

	if err := h.Gen.WriteAccessKey(machineName, key.ID, req.PublicKey); err != nil {
		log.Printf("error writing access key file: %v", err)
	}

	if err := h.regenerateConfig(); err != nil {
		log.Printf("error regenerating config: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(key)
}

func (h *Handlers) ListAccessKeys(w http.ResponseWriter, r *http.Request) {
	machineName := chi.URLParam(r, "name")

	machine, err := h.DB.GetMachine(machineName)
	if err != nil {
		log.Printf("error getting machine: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if machine == nil {
		jsonError(w, "machine not found", http.StatusNotFound)
		return
	}

	keys, err := h.DB.ListAccessKeys(machineName)
	if err != nil {
		log.Printf("error listing access keys: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = []db.AccessKey{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(keys)
}

func (h *Handlers) DeleteAccessKey(w http.ResponseWriter, r *http.Request) {
	machineName := chi.URLParam(r, "name")
	keyIDStr := chi.URLParam(r, "keyID")
	var keyID int64
	if _, err := fmt.Sscanf(keyIDStr, "%d", &keyID); err != nil {
		jsonError(w, "invalid key id", http.StatusBadRequest)
		return
	}

	// Verify the key belongs to this machine
	key, err := h.DB.GetAccessKey(keyID)
	if err != nil {
		log.Printf("error getting access key: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if key == nil || key.MachineName != machineName {
		jsonError(w, "access key not found", http.StatusNotFound)
		return
	}

	if err := h.DB.DeleteAccessKey(keyID); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	_ = h.Gen.RemoveAccessKey(machineName, keyID)

	if err := h.regenerateConfig(); err != nil {
		log.Printf("error regenerating config: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) regenerateConfig() error {
	machines, err := h.DB.ListMachines()
	if err != nil {
		return err
	}

	// Build pipe entries with access keys for each machine
	var entries []config.PipeEntry
	for _, m := range machines {
		accessKeys, err := h.DB.ListAccessKeys(m.Name)
		if err != nil {
			log.Printf("warning: failed to list access keys for %s: %v", m.Name, err)
		}
		// Write access key files
		for _, ak := range accessKeys {
			if err := h.Gen.WriteAccessKey(m.Name, ak.ID, ak.PublicKey); err != nil {
				log.Printf("warning: failed to write access key %d: %v", ak.ID, err)
			}
		}
		entries = append(entries, config.PipeEntry{Machine: m, AccessKeys: accessKeys})
	}

	if err := h.Gen.Generate(entries); err != nil {
		return err
	}
	if err := h.Gen.UpdateAuthorizedKeys(machines); err != nil {
		log.Printf("warning: failed to update authorized_keys: %v", err)
	}
	if h.OnChange != nil {
		h.OnChange()
	}
	return nil
}
