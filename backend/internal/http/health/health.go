// Package health implements the /health and /ready endpoints.
//
//   - /health  — liveness: the process is up and can serve traffic.
//   - /ready   — readiness: every dependency the process needs is reachable.
//
// Both report the status of individual dependencies so that operators can see
// *why* a replica is not ready, without exposing secrets.
package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/akqqqw/channelhunter/backend/internal/observability"
	"github.com/akqqqw/channelhunter/backend/internal/platform/httpx"
)

// Status is the health of a single dependency.
type Status string

// Dependency states.
const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

// Check probes one dependency.
type Check struct {
	// Name is the identifier reported in the response.
	Name string
	// Critical dependencies make the whole readiness probe fail.
	Critical bool
	// Timeout bounds the probe; defaults to 2s.
	Timeout time.Duration
	// Probe performs the actual check.
	Probe func(ctx context.Context) error
}

// Report is the JSON body returned by the endpoints.
type Report struct {
	Status       Status                `json:"status"`
	Service      string                `json:"service"`
	Process      string                `json:"process"`
	Version      string                `json:"version"`
	Environment  string                `json:"environment"`
	Uptime       string                `json:"uptime"`
	Time         time.Time             `json:"time"`
	Dependencies map[string]Dependency `json:"dependencies"`
}

// Dependency is a single probe result.
type Dependency struct {
	Status    Status  `json:"status"`
	Critical  bool    `json:"critical"`
	LatencyMS float64 `json:"latency_ms"`
	Error     string  `json:"error,omitempty"`
	LastCheck string  `json:"checked_at"`
}

// Checker runs a fixed set of dependency probes.
type Checker struct {
	service     string
	process     string
	version     string
	environment string
	startedAt   time.Time
	checks      []Check
	log         *slog.Logger
}

// NewChecker builds a checker.
func NewChecker(service, process, version, environment string, startedAt time.Time, log *slog.Logger, checks ...Check) *Checker {
	return &Checker{
		service:     service,
		process:     process,
		version:     version,
		environment: environment,
		startedAt:   startedAt,
		checks:      checks,
		log:         log,
	}
}

// Run executes all probes concurrently and returns the aggregated report.
func (c *Checker) Run(ctx context.Context) Report {
	start := time.Now()
	report := Report{
		Status:       StatusOK,
		Service:      c.service,
		Process:      c.process,
		Version:      c.version,
		Environment:  c.environment,
		Uptime:       time.Since(c.startedAt).Round(time.Second).String(),
		Time:         start.UTC(),
		Dependencies: make(map[string]Dependency, len(c.checks)),
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		critical int
	)
	for _, check := range c.checks {
		wg.Add(1)
		go func(ch Check) {
			defer wg.Done()
			timeout := ch.Timeout
			if timeout <= 0 {
				timeout = 2 * time.Second
			}
			probeCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			probeStart := time.Now()
			err := ch.Probe(probeCtx)
			dep := Dependency{
				Status:    StatusOK,
				Critical:  ch.Critical,
				LatencyMS: float64(time.Since(probeStart).Microseconds()) / 1000,
				LastCheck: time.Now().UTC().Format(time.RFC3339),
			}
			if err != nil {
				dep.Status = StatusDown
				// Error strings are trimmed and never include credentials:
				// probes wrap only network-level failures.
				dep.Error = sanitize(err)
			}

			mu.Lock()
			report.Dependencies[ch.Name] = dep
			if dep.Status != StatusOK && ch.Critical {
				critical++
			}
			mu.Unlock()
		}(check)
	}
	wg.Wait()

	switch {
	case critical > 0:
		report.Status = StatusDown
	case len(report.Dependencies) > 0:
		for _, dep := range report.Dependencies {
			if dep.Status != StatusOK {
				report.Status = StatusDegraded
				break
			}
		}
	}

	return report
}

func sanitize(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return err.Error()
}

// LivenessHandler answers /health. It never touches dependencies: a slow
// database must not cause a container restart loop.
func (c *Checker) LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, r, http.StatusOK, map[string]any{
			"status":  StatusOK,
			"service": c.service,
			"process": c.process,
			"version": c.version,
			"uptime":  time.Since(c.startedAt).Round(time.Second).String(),
			"time":    time.Now().UTC(),
		})
	})
}

// ReadinessHandler answers /ready and fails with 503 when a critical
// dependency is unavailable.
func (c *Checker) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		report := c.Run(r.Context())
		status := http.StatusOK
		if report.Status == StatusDown {
			status = http.StatusServiceUnavailable
			observability.LoggerFrom(r.Context()).Warn("readiness check failed",
				slog.String("status", string(report.Status)))
		}
		httpx.JSON(w, r, status, report)
	})
}

// ReadinessReport exposes the report for tests and the admin endpoint.
func (c *Checker) ReadinessReport(ctx context.Context) Report { return c.Run(ctx) }
