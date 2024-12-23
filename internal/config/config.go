package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the application.
type Config struct {
	App     AppConfig
	HTTP    HTTPConfig
	GRPC    GRPCConfig
	Logger  LoggerConfig
	DB      DatabaseConfig
	Kafka   KafkaConfig
	Program ProgramConfig
}

type AppConfig struct {
	Name        string
	Version     string
	Environment string
}

// HTTPConfig holds the health HTTP listener.
type HTTPConfig struct {
	Port string
}

type GRPCConfig struct {
	Port            string
	ShutdownTimeout time.Duration
}

type LoggerConfig struct {
	Level    string
	Format   string
	Output   string
	FilePath string
	Service  string
}

type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
	// AllowInsecure permits DB_SSL_MODE=disable in production. Only for a
	// database on the same private network (e.g. the compose stack).
	AllowInsecure   bool
	MaxConns        int
	MinConns        int
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
	AutoMigrate     bool
}

// KafkaConfig: empty Brokers disables the order-event consumer (points
// are then neither earned nor refunded automatically).
type KafkaConfig struct {
	Brokers     []string
	GroupID     string
	OrdersTopic string
}

// ProgramConfig holds the loyalty rules.
type ProgramConfig struct {
	EarnPercent       int           // points per 100 ₽ paid
	MaxRedeemPercent  int           // max share of an order payable with points
	WelcomeBonus      int           // points on sign-up
	PointValueKopecks int64         // 100 = 1 point is worth 1 ₽
	HoldTTL           time.Duration // how long a checkout may hold points
	CodeTTL           time.Duration // kiosk code lifetime
}

func Load() (*Config, error) {
	env := getEnv("APP_ENV", "development")
	loadDotenv(env)

	cfg := &Config{
		App: AppConfig{
			Name:        getEnv("APP_NAME", "kfc-loyalty"),
			Version:     getEnv("APP_VERSION", "1.0.0"),
			Environment: env,
		},
		HTTP: HTTPConfig{Port: getEnv("HTTP_PORT", "8004")},
		GRPC: GRPCConfig{
			Port:            getEnv("GRPC_PORT", "9104"),
			ShutdownTimeout: getEnvDuration("GRPC_SHUTDOWN_TIMEOUT", 10*time.Second),
		},
		Logger: LoggerConfig{
			Level:    getEnv("LOG_LEVEL", defaultLogLevel(env)),
			Format:   getEnv("LOG_FORMAT", defaultLogFormat(env)),
			Output:   getEnv("LOG_OUTPUT", "stdout"),
			FilePath: getEnv("LOG_FILE_PATH", "logs/app.log"),
			Service:  getEnv("LOG_SERVICE", "kfc-loyalty"),
		},
		DB: DatabaseConfig{
			Host:            getEnv("DB_HOST", "localhost"),
			Port:            getEnv("DB_PORT", "5432"),
			User:            getEnv("DB_USER", "postgres"),
			Password:        getEnv("DB_PASSWORD", "postgres"),
			Name:            getEnv("DB_NAME", "kfc_loyalty"),
			SSLMode:         getEnv("DB_SSL_MODE", "disable"),
			AllowInsecure:   getEnvBool("DB_ALLOW_INSECURE", false),
			MaxConns:        getEnvInt("DB_MAX_CONNS", 20),
			MinConns:        getEnvInt("DB_MIN_CONNS", 5),
			MaxConnLifetime: getEnvDuration("DB_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime: getEnvDuration("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
			ConnectTimeout:  getEnvDuration("DB_CONNECT_TIMEOUT", 10*time.Second),
			AutoMigrate:     getEnvBool("DB_AUTO_MIGRATE", true),
		},
		Kafka: KafkaConfig{
			Brokers:     getEnvSlice("KAFKA_BROKERS", []string{}),
			GroupID:     getEnv("KAFKA_GROUP_ID", "kfc-loyalty"),
			OrdersTopic: getEnv("KAFKA_ORDERS_TOPIC", "orders.events"),
		},
		Program: ProgramConfig{
			EarnPercent:       getEnvInt("LOYALTY_EARN_PERCENT", 5),
			MaxRedeemPercent:  getEnvInt("LOYALTY_MAX_REDEEM_PERCENT", 50),
			WelcomeBonus:      getEnvInt("LOYALTY_WELCOME_BONUS", 100),
			PointValueKopecks: int64(getEnvInt("LOYALTY_POINT_VALUE_KOPECKS", 100)),
			HoldTTL:           getEnvDuration("LOYALTY_HOLD_TTL", 15*time.Minute),
			CodeTTL:           getEnvDuration("LOYALTY_CODE_TTL", 5*time.Minute),
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	var errs []string
	switch c.App.Environment {
	case "development", "staging", "production":
	default:
		errs = append(errs, fmt.Sprintf("invalid APP_ENV: %q", c.App.Environment))
	}
	p := c.Program
	if p.EarnPercent < 0 || p.EarnPercent > 100 {
		errs = append(errs, "LOYALTY_EARN_PERCENT must be 0–100")
	}
	if p.MaxRedeemPercent < 0 || p.MaxRedeemPercent > 100 {
		errs = append(errs, "LOYALTY_MAX_REDEEM_PERCENT must be 0–100")
	}
	if p.WelcomeBonus < 0 {
		errs = append(errs, "LOYALTY_WELCOME_BONUS must not be negative")
	}
	if p.PointValueKopecks <= 0 {
		errs = append(errs, "LOYALTY_POINT_VALUE_KOPECKS must be positive")
	}
	if p.HoldTTL < time.Minute || p.CodeTTL < 30*time.Second {
		errs = append(errs, "LOYALTY_HOLD_TTL must be ≥1m and LOYALTY_CODE_TTL ≥30s")
	}
	if c.IsProduction() && c.DB.SSLMode == "disable" && !c.DB.AllowInsecure {
		errs = append(errs, "DB_SSL_MODE should not be 'disable' in production (or set DB_ALLOW_INSECURE=true for a database on a private network)")
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (c *Config) IsProduction() bool { return c.App.Environment == "production" }

func (c *Config) DSN() string {
	// URL form: user and password are percent-encoded, so secrets may
	// contain spaces, quotes or any other character.
	q := url.Values{
		"sslmode":         {c.DB.SSLMode},
		"connect_timeout": {strconv.Itoa(int(c.DB.ConnectTimeout.Seconds()))},
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.DB.User, c.DB.Password),
		Host:     net.JoinHostPort(c.DB.Host, c.DB.Port),
		Path:     "/" + c.DB.Name,
		RawQuery: q.Encode(),
	}
	return u.String()
}

func defaultLogLevel(env string) string {
	if env == "production" {
		return "info"
	}
	return "debug"
}

func defaultLogFormat(env string) string {
	if env == "production" {
		return "json"
	}
	return "text"
}
