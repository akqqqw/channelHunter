// Package routes wires the HTTP router: middleware chain, health endpoints and
// the versioned /api/v1 surface. Handlers stay thin and delegate to services.
package routes

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/akqqqw/channelhunter/backend/internal/config"
	"github.com/akqqqw/channelhunter/backend/internal/http/health"
	"github.com/akqqqw/channelhunter/backend/internal/http/middleware"
	"github.com/akqqqw/channelhunter/backend/internal/platform/httpx"
)

// Deps are the collaborators the router needs.
type Deps struct {
	Config  *config.Config
	Logger  *slog.Logger
	Health  *health.Checker
	Version string
	// RootContext is cancelled on shutdown; handler contexts derive from it.
	TrustedProxies []*net.IPNet
	// RateLimiter is shared by sensitive endpoints.
	RateLimiter *middleware.RateLimiter
}

// New builds the root HTTP handler.
func New(deps Deps) http.Handler {
	cfg := deps.Config
	log := deps.Logger

	r := chi.NewRouter()

	// Global chain. Order matters: recovery wraps everything, so a panic in a
	// later middleware still results in a clean 500 with the right headers.
	r.Use(middleware.Recover())
	r.Use(middleware.RequestID(log))
	r.Use(middleware.AccessLog(deps.TrustedProxies))
	r.Use(middleware.SecurityHeaders())

	if len(cfg.HTTP.CORSAllowedOrigins) > 0 {
		r.Use(middleware.CORS(cfg.HTTP.CORSAllowedOrigins))
	}

	r.NotFound(middleware.NotFound().ServeHTTP)
	r.MethodNotAllowed(middleware.MethodNotAllowed().ServeHTTP)

	// Liveness/readiness are outside the API prefix so probes stay stable.
	r.Get("/health", deps.Health.LivenessHandler().ServeHTTP)
	r.Get("/ready", deps.Health.ReadinessHandler().ServeHTTP)
	// Common alternative probe paths used by orchestrators.
	r.Get("/healthz", deps.Health.LivenessHandler().ServeHTTP)
	r.Get("/livez", deps.Health.LivenessHandler().ServeHTTP)
	r.Get("/readyz", deps.Health.ReadinessHandler().ServeHTTP)

	mountAPI(r, deps)

	return r
}

func mountAPI(r chi.Router, deps Deps) {
	cfg := deps.Config

	r.Route("/api/v1", func(api chi.Router) {
		// Per-IP throttle for unauthenticated surface. Authentication-specific
		// limits are added per route as they are implemented.
		if cfg.RateLimit.Enabled && deps.RateLimiter != nil {
			api.Use(middleware.RateLimit(deps.RateLimiter, deps.TrustedProxies, nil))
		}

		api.Get("/meta", func(w http.ResponseWriter, req *http.Request) {
			httpx.JSON(w, req, http.StatusOK, map[string]any{
				"service":     "channelhunter",
				"version":     deps.Version,
				"environment": string(cfg.Environment),
				"api_version": "v1",
				"time":        time.Now().UTC(),
			})
		})

		// Stage 2+ routes are declared by their own mount functions; the
		// placeholder keeps 501s explicit instead of a bare 404 while the
		// feature is being built.
	})
}
