package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// ============================================================
// Configuration management
//
// IMPORTANT: Viper uses mapstructure tags for Unmarshal,
// NOT yaml tags. We use both for compatibility.
//
// Key config for base64 image handling:
//   gateway.public_url - Must be a URL that external providers
//   (like Agnes AI) can reach to fetch temp images.
//   Example: "https://my-gateway.example.com"
//
//   If not set, temp images won't work for providers that
//   require URLs (as opposed to accepting base64 directly).
// ============================================================

// Config holds the application configuration
type Config struct {
	Server   ServerConfig               `yaml:"server" mapstructure:"server"`
	Gateway  GatewayConfig              `yaml:"gateway" mapstructure:"gateway"`
	Auth     AuthConfig                 `yaml:"auth" mapstructure:"auth"`
	Providers map[string]ProviderConfig `yaml:"providers" mapstructure:"providers"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Port int    `yaml:"port" json:"port" mapstructure:"port"`
	Host string `yaml:"host" json:"host" mapstructure:"host"`
	Mode string `yaml:"mode" json:"mode" mapstructure:"mode"` // debug, release, test
}

// GatewayConfig holds gateway-level configuration
type GatewayConfig struct {
	// PublicURL is the externally accessible URL of this gateway.
	// Used for generating temp image URLs that providers like Agnes AI can fetch.
	// MUST be set if you want base64 image support (图生图/图生视频等).
	// Example: "https://my-gateway.example.com" or "http://123.45.67.89:8080"
	PublicURL string `yaml:"public_url" json:"public_url" mapstructure:"public_url"`

	// TempImageTTL is how long converted base64 images stay available (in minutes)
	TempImageTTL int `yaml:"temp_image_ttl" json:"temp_image_ttl" mapstructure:"temp_image_ttl"`

	// TempImageCleanupInterval is how often expired images are cleaned up (in minutes)
	TempImageCleanupInterval int `yaml:"temp_image_cleanup_interval" json:"temp_image_cleanup_interval" mapstructure:"temp_image_cleanup_interval"`
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	Enabled bool              `yaml:"enabled" json:"enabled" mapstructure:"enabled"`
	Keys    map[string]string `yaml:"keys" json:"keys" mapstructure:"keys"` // api_key -> description
}

// ProviderConfig holds provider configuration
type ProviderConfig struct {
	BaseURL  string            `yaml:"base_url" json:"base_url" mapstructure:"base_url"`
	APIKey   string            `yaml:"api_key" json:"api_key" mapstructure:"api_key"`
	Enabled  bool              `yaml:"enabled" json:"enabled" mapstructure:"enabled"`
	Models   []ModelConfig     `yaml:"models" json:"models" mapstructure:"models"`
	Extra    map[string]string `yaml:"extra" json:"extra" mapstructure:"extra"`
}

// ModelConfig holds model mapping configuration
type ModelConfig struct {
	ExternalModel string   `yaml:"external_model" json:"external_model" mapstructure:"external_model"`
	ProviderModel string   `yaml:"provider_model" json:"provider_model" mapstructure:"provider_model"`
	Capabilities  []string `yaml:"capabilities" json:"capabilities" mapstructure:"capabilities"`
}

// Load loads configuration from file and environment variables
func Load(configPath string, logger *zap.Logger) (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.mode", "release")
	v.SetDefault("auth.enabled", false)
	v.SetDefault("gateway.temp_image_ttl", 10)
	v.SetDefault("gateway.temp_image_cleanup_interval", 5)

	// Read config file
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./configs")
	}

	// Environment variable support
	v.SetEnvPrefix("O4OPENAI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(*os.PathError); ok {
			logger.Warn("No config file found, using defaults")
		} else {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Override API keys from environment variables
	// Format: O4OPENAI_PROVIDERS_<NAME>_APIKEY
	for name, pc := range cfg.Providers {
		envKey := fmt.Sprintf("O4OPENAI_PROVIDERS_%s_APIKEY", strings.ToUpper(name))
		if envVal := os.Getenv(envKey); envVal != "" {
			pc.APIKey = envVal
			cfg.Providers[name] = pc
		}
	}

	// Override public_url from environment
	if envVal := os.Getenv("O4OPENAI_GATEWAY_PUBLIC_URL"); envVal != "" {
		cfg.Gateway.PublicURL = envVal
	}

	return cfg, nil
}
