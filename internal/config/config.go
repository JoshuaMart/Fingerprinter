package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Scanner    ScannerConfig    `yaml:"scanner"`
	Browser    BrowserConfig    `yaml:"browser"`
	Detections DetectionsConfig `yaml:"detections"`
}

type ServerConfig struct {
	Port        int           `yaml:"port"`
	ReadTimeout time.Duration `yaml:"read_timeout"`
}

type ScannerConfig struct {
	MaxRedirects    int               `yaml:"max_redirects"`
	RequestTimeout  time.Duration     `yaml:"request_timeout"`
	Headers         map[string]string `yaml:"headers"`
	ConcurrentScans int               `yaml:"concurrent_scans"`
	Proxy           string            `yaml:"proxy"`
}

type BrowserConfig struct {
	Enabled     bool          `yaml:"enabled"`
	PoolSize    int           `yaml:"pool_size"`
	PageTimeout time.Duration `yaml:"page_timeout"`
}

type DetectionsConfig struct {
	YAMLDir string `yaml:"yaml_dir"`
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Port:        3001,
			ReadTimeout: 30 * time.Second,
		},
		Scanner: ScannerConfig{
			MaxRedirects:   10,
			RequestTimeout: 10 * time.Second,
			Headers: map[string]string{
				"User-Agent": "Fingerprinter/1.0",
			},
			ConcurrentScans: 50,
		},
		Browser: BrowserConfig{
			Enabled:     true,
			PoolSize:    5,
			PageTimeout: 15 * time.Second,
		},
		Detections: DetectionsConfig{
			YAMLDir: "./detections/",
		},
	}
}

// Load reads configuration from a YAML file (if provided) and overrides with environment variables.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	loadEnvOverrides(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

func loadEnvOverrides(cfg *Config) {
	if v := os.Getenv("FINGERPRINTER_SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("FINGERPRINTER_SCANNER_USER_AGENT"); v != "" {
		if cfg.Scanner.Headers == nil {
			cfg.Scanner.Headers = make(map[string]string)
		}
		cfg.Scanner.Headers["User-Agent"] = v
	}
	if v := os.Getenv("FINGERPRINTER_BROWSER_ENABLED"); v != "" {
		cfg.Browser.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("FINGERPRINTER_DETECTIONS_YAML_DIR"); v != "" {
		cfg.Detections.YAMLDir = v
	}
	if v := os.Getenv("FINGERPRINTER_SCANNER_PROXY"); v != "" {
		cfg.Scanner.Proxy = v
	}
}

func validate(cfg *Config) error {
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", cfg.Server.Port)
	}
	if cfg.Scanner.MaxRedirects < 1 || cfg.Scanner.MaxRedirects > 30 {
		return fmt.Errorf("scanner.max_redirects must be between 1 and 30, got %d", cfg.Scanner.MaxRedirects)
	}
	if cfg.Scanner.ConcurrentScans < 1 {
		return fmt.Errorf("scanner.concurrent_scans must be at least 1")
	}
	if cfg.Browser.PoolSize < 1 {
		return fmt.Errorf("browser.pool_size must be at least 1")
	}
	return nil
}
