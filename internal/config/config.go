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
	Redis      RedisConfig      `yaml:"redis"`
}

type ServerConfig struct {
	Port        int           `yaml:"port"`
	ReadTimeout time.Duration `yaml:"read_timeout"`
}

type ScannerConfig struct {
	MaxRedirects    int               `yaml:"max_redirects"`
	RequestTimeout  time.Duration     `yaml:"request_timeout"`
	Headers         map[string]string `yaml:"headers"`
	UserHeaders     map[string]string `yaml:"-"`
	ConcurrentScans int               `yaml:"concurrent_scans"`
	Proxy           string            `yaml:"proxy"`
}

type BrowserConfig struct {
	PoolSize    int           `yaml:"pool_size"`
	PageTimeout time.Duration `yaml:"page_timeout"`
	ControlURL  string        `yaml:"control_url"`
}

type DetectionsConfig struct {
	YAMLDir string `yaml:"yaml_dir"`
}

type RedisConfig struct {
	URL    string `yaml:"url"`
	Stream string `yaml:"stream"`
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
			PoolSize:    5,
			PageTimeout: 15 * time.Second,
			ControlURL:  "http://localhost:9222",
		},
		Detections: DetectionsConfig{
			YAMLDir: "./detections/",
		},
		Redis: RedisConfig{
			Stream: "scans",
		},
	}
}

// Load reads configuration from a YAML file (if provided) and overrides with environment variables.
func Load(path string) (*Config, error) {
	cfg := defaults()

	// Track which headers are explicitly set by the user (YAML + env).
	// The browser should keep its native User-Agent unless the user overrides it,
	// while the HTTP client always uses the default Fingerprinter UA.
	var userCfg struct {
		Scanner struct {
			Headers map[string]string `yaml:"headers"`
		} `yaml:"scanner"`
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
		// Parse again into a clean struct to isolate user-defined headers.
		_ = yaml.Unmarshal(data, &userCfg)
	}

	cfg.Scanner.UserHeaders = copyHeaders(userCfg.Scanner.Headers)

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
		if cfg.Scanner.UserHeaders == nil {
			cfg.Scanner.UserHeaders = make(map[string]string)
		}
		cfg.Scanner.UserHeaders["User-Agent"] = v
	}
	if v := os.Getenv("FINGERPRINTER_BROWSER_CONTROL_URL"); v != "" {
		cfg.Browser.ControlURL = v
	}
	if v := os.Getenv("FINGERPRINTER_DETECTIONS_YAML_DIR"); v != "" {
		cfg.Detections.YAMLDir = v
	}
	if v := os.Getenv("FINGERPRINTER_SCANNER_PROXY"); v != "" {
		cfg.Scanner.Proxy = v
	}
	if v := os.Getenv("FINGERPRINTER_REDIS_URL"); v != "" {
		cfg.Redis.URL = v
	}
	if v := os.Getenv("FINGERPRINTER_REDIS_STREAM"); v != "" {
		cfg.Redis.Stream = v
	}
}

func copyHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
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
	if cfg.Browser.ControlURL == "" {
		return fmt.Errorf("browser.control_url must not be empty")
	}
	return nil
}
