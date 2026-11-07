// Package config loads and validates the runtime configuration of every
// ChannelHunter process (api, bot, worker) from environment variables.
//
// Configuration is intentionally explicit: every value has a documented
// default (see .env.example) and validation fails fast on startup so that a
// misconfigured deployment never runs in a half-working state.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Process identifies which executable is booting.
type Process string

// Supported processes.
const (
	ProcessAPI    Process = "api"
	ProcessBot    Process = "bot"
	ProcessWorker Process = "worker"
)

// Environment names the deployment environment.
type Environment string

// Supported environments.
const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

// IsProduction reports whether the environment is production.
func (e Environment) IsProduction() bool { return e == EnvProduction }

// Config is the fully resolved configuration of a process.
type Config struct {
	Process     Process
	Environment Environment

	HTTP      HTTPConfig
	Database  DatabaseConfig
	Log       LogConfig
	Telegram  TelegramConfig
	Auth      AuthConfig
	RateLimit RateLimitConfig
	App       AppConfig
}

// HTTPConfig configures the public HTTP API.
type HTTPConfig struct {
	Host string
	Port int
	// ReadTimeout / WriteTimeout / IdleTimeout guard against slow clients.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	// ShutdownTimeout is how long in-flight requests get to finish on SIGTERM.
	ShutdownTimeout time.Duration
	// TrustedProxies lists CIDRs whose X-Forwarded-* headers are trusted.
	TrustedProxies []string
	// CORSAllowedOrigins is empty in production by default; the Mini App is
	// served from the same origin and therefore needs no CORS exception.
	CORSAllowedOrigins []string
}

// Addr returns the listen address.
func (c HTTPConfig) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// DatabaseConfig configures the PostgreSQL connection pool.
type DatabaseConfig struct {
	URL string
	// MaxConns / MinConns bound the pgx pool size.
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
	// MigrationsPath points at the directory holding golang-migrate files.
	MigrationsPath string
	// ApplyOnStart runs migrations before the process starts serving.
	ApplyOnStart bool
}

// LogConfig configures structured logging.
type LogConfig struct {
	Level  slog.Level
	Format string // "json" | "text"
}

// TelegramConfig holds Telegram Bot API credentials.
type TelegramConfig struct {
	BotToken string
	// Mode is "webhook" or "polling". Webhooks are required in production.
	Mode string
	// WebhookSecretToken is validated against the X-Telegram-Bot-Api-Secret-Token
	// header on every webhook call (requirement: verify Telegram webhook).
	WebhookSecretToken string
	// WebhookURL is the public HTTPS endpoint registered with setWebhook.
	WebhookURL string
	// AdminIDs may use administrative commands; optional.
	AdminIDs []int64
}

// AuthConfig configures API authentication.
type AuthConfig struct {
	// JWTSecret signs session tokens issued by POST /api/v1/auth/telegram.
	JWTSecret string
	// TokenTTL is the lifetime of an issued session token.
	TokenTTL time.Duration
	// MiniAppInitDataTTL is the maximum accepted age of Telegram initData.
	MiniAppInitDataTTL time.Duration
	// BotUsername is used to build Mini App deep links.
	BotUsername string
}

// RateLimitConfig configures request throttling for sensitive endpoints.
type RateLimitConfig struct {
	Enabled bool
	// Requests per window, per client key.
	Requests int
	Window   time.Duration
}

// AppConfig holds product-level settings that are configuration, not code.
type AppConfig struct {
	// PublicBaseURL is used in generated links and reports.
	PublicBaseURL string
	// Billing currency for plan prices (ISO-4217).
	BillingCurrency string
	// Timezone used for daily aggregation boundaries.
	Timezone string
	// MaxUploadBytes limits CSV/XLSX import uploads.
	MaxUploadBytes int64
	// ProviderTimeout bounds every outbound provider call.
	ProviderTimeout time.Duration
}

// Load builds the configuration for the given process.
func Load(process Process) (*Config, error) {
	env := Environment(strings.ToLower(getString("APP_ENV", string(EnvDevelopment))))

	cfg := &Config{
		Process:     process,
		Environment: env,
		HTTP: HTTPConfig{
			Host:               getString("HTTP_HOST", "0.0.0.0"),
			Port:               getInt("HTTP_PORT", 8080),
			ReadTimeout:        getDuration("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:       getDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:        getDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout:    getDuration("HTTP_SHUTDOWN_TIMEOUT", 20*time.Second),
			TrustedProxies:     getCSV("HTTP_TRUSTED_PROXIES"),
			CORSAllowedOrigins: getCSV("HTTP_CORS_ALLOWED_ORIGINS"),
		},
		Database: DatabaseConfig{
			URL:               getString("DATABASE_URL", ""),
			MaxConns:          int32(getInt("DATABASE_MAX_CONNS", 10)),
			MinConns:          int32(getInt("DATABASE_MIN_CONNS", 1)),
			MaxConnLifetime:   getDuration("DATABASE_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime:   getDuration("DATABASE_MAX_CONN_IDLE_TIME", 30*time.Minute),
			HealthCheckPeriod: getDuration("DATABASE_HEALTHCHECK_PERIOD", time.Minute),
			ConnectTimeout:    getDuration("DATABASE_CONNECT_TIMEOUT", 10*time.Second),
			MigrationsPath:    getString("DATABASE_MIGRATIONS_PATH", "migrations"),
			ApplyOnStart:      getBool("DATABASE_MIGRATE_ON_START", false),
		},
		Log: LogConfig{
			Level:  getLogLevel("LOG_LEVEL", slog.LevelInfo),
			Format: getString("LOG_FORMAT", "json"),
		},
		Telegram: TelegramConfig{
			BotToken:           getString("TELEGRAM_BOT_TOKEN", ""),
			Mode:               strings.ToLower(getString("TELEGRAM_MODE", "polling")),
			WebhookSecretToken: getString("TELEGRAM_WEBHOOK_SECRET_TOKEN", ""),
			WebhookURL:         getString("TELEGRAM_WEBHOOK_URL", ""),
			AdminIDs:           getInt64CSV("TELEGRAM_ADMIN_IDS"),
		},
		Auth: AuthConfig{
			JWTSecret:          getString("AUTH_JWT_SECRET", ""),
			TokenTTL:           getDuration("AUTH_TOKEN_TTL", 24*time.Hour),
			MiniAppInitDataTTL: getDuration("AUTH_INITDATA_TTL", 24*time.Hour),
			BotUsername:        getString("TELEGRAM_BOT_USERNAME", ""),
		},
		RateLimit: RateLimitConfig{
			Enabled:  getBool("RATE_LIMIT_ENABLED", true),
			Requests: getInt("RATE_LIMIT_REQUESTS", 60),
			Window:   getDuration("RATE_LIMIT_WINDOW", time.Minute),
		},
		App: AppConfig{
			PublicBaseURL:   getString("PUBLIC_BASE_URL", "http://localhost:8080"),
			BillingCurrency: getString("BILLING_CURRENCY", "RUB"),
			Timezone:        getString("APP_TIMEZONE", "Europe/Moscow"),
			MaxUploadBytes:  int64(getInt("MAX_UPLOAD_BYTES", 10<<20)),
			ProviderTimeout: getDuration("PROVIDER_TIMEOUT", 20*time.Second),
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFromEnvFile loads variables from the given .env file (if present) and
// then resolves the configuration. Existing environment variables win.
func LoadFromEnvFile(process Process, envFile string) (*Config, error) {
	if envFile != "" {
		if err := loadEnvFile(envFile); err != nil {
			return nil, err
		}
	}
	return Load(process)
}

// Validate checks invariants that must hold for the process to run safely.
func (c *Config) Validate() error {
	var problems []string

	if c.Process != ProcessAPI && c.Process != ProcessBot && c.Process != ProcessWorker {
		problems = append(problems, fmt.Sprintf("unknown process %q", c.Process))
	}
	if c.Database.URL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	if c.HTTP.Port <= 0 || c.HTTP.Port > 65535 {
		problems = append(problems, "HTTP_PORT must be within 1..65535")
	}
	if c.Database.MaxConns < c.Database.MinConns {
		problems = append(problems, "DATABASE_MAX_CONNS must be >= DATABASE_MIN_CONNS")
	}
	switch c.Log.Format {
	case "json", "text":
	default:
		problems = append(problems, `LOG_FORMAT must be "json" or "text"`)
	}

	if c.Process == ProcessBot {
		if c.Telegram.BotToken == "" {
			problems = append(problems, "TELEGRAM_BOT_TOKEN is required for the bot process")
		}
		switch c.Telegram.Mode {
		case "polling":
			if c.Environment.IsProduction() {
				problems = append(problems, "TELEGRAM_MODE=polling is not allowed in production, use webhook")
			}
		case "webhook":
			if c.Telegram.WebhookSecretToken == "" {
				problems = append(problems, "TELEGRAM_WEBHOOK_SECRET_TOKEN is required in webhook mode")
			}
			if c.Telegram.WebhookURL == "" {
				problems = append(problems, "TELEGRAM_WEBHOOK_URL is required in webhook mode")
			}
		default:
			problems = append(problems, `TELEGRAM_MODE must be "polling" or "webhook"`)
		}
	}

	if c.Process == ProcessAPI {
		if len(c.Auth.JWTSecret) < 32 && c.Environment.IsProduction() {
			problems = append(problems, "AUTH_JWT_SECRET must be at least 32 characters in production")
		}
		if c.Auth.JWTSecret == "" {
			problems = append(problems, "AUTH_JWT_SECRET is required for the api process")
		}
		// The Mini App authenticates with Telegram initData, which is signed
		// with the bot token; without a token no login flow can be verified.
		if c.Telegram.BotToken == "" {
			problems = append(problems, "TELEGRAM_BOT_TOKEN is required for the api process")
		}
		if c.RateLimit.Enabled && (c.RateLimit.Requests <= 0 || c.RateLimit.Window <= 0) {
			problems = append(problems, "RATE_LIMIT_REQUESTS and RATE_LIMIT_WINDOW must be positive")
		}
	}

	if c.App.MaxUploadBytes <= 0 {
		problems = append(problems, "MAX_UPLOAD_BYTES must be positive")
	}
	// net/http keeps a freshly accepted connection (StateNew) out of the "idle"
	// set for 5 seconds, so a shutdown budget at or below that can be exceeded
	// by a client that opens a socket and sends nothing.
	if c.HTTP.ShutdownTimeout < 6*time.Second {
		problems = append(problems, "HTTP_SHUTDOWN_TIMEOUT must be at least 6s (net/http treats brand-new connections as busy for 5s)")
	}
	if _, err := time.LoadLocation(c.App.Timezone); err != nil {
		problems = append(problems, fmt.Sprintf("APP_TIMEZONE %q is not a valid IANA timezone", c.App.Timezone))
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

func getString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func getBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func getDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return d
}

func getCSV(key string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getInt64CSV(key string) []int64 {
	raw := getCSV(key)
	out := make([]int64, 0, len(raw))
	for _, r := range raw {
		if n, err := strconv.ParseInt(r, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func getLogLevel(key string, def slog.Level) slog.Level {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.TrimSpace(v))); err != nil {
		return def
	}
	return lvl
}
