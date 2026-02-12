package server

import (
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/LipJ01/fly-ssh-bastion/internal/config"
	"github.com/LipJ01/fly-ssh-bastion/internal/db"
)

func NewRouter(database *db.DB, gen *config.Generator, apiSecret, serverURL string, onChange func()) *chi.Mux {
	h := &Handlers{
		DB:        database,
		Gen:       gen,
		OnChange:  onChange,
		ServerURL: serverURL,
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Global rate limit: 100 requests per minute per IP
	r.Use(httprate.LimitByIP(100, time.Minute))

	// Public
	r.Get("/api/status", h.Status)

	// Authenticated
	r.Group(func(r chi.Router) {
		// Stricter limit on authenticated endpoints: 20 per minute per IP
		r.Use(httprate.LimitByIP(20, time.Minute))
		r.Use(apiKeyAuth(apiSecret))
		r.Post("/api/register", h.Register)
		r.Get("/api/machines", h.ListMachines)
		r.Delete("/api/machines/{name}", h.DeleteMachine)
		r.Put("/api/machines/{name}/rename", h.RenameMachine)
		r.Post("/api/heartbeat", h.Heartbeat)
	})

	return r
}
