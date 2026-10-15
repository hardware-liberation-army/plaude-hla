package config

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aplaud/internal/crypto"
	"gopkg.in/yaml.v3"
)

type WhisperConfig struct {
	ModelPath      string `yaml:"model_path"`
	Language       string `yaml:"language"`
	LibPath        string `yaml:"lib_path"`
	GGMLLibPath    string `yaml:"ggml_lib_path"`
	WhisperCliPath string `yaml:"whisper_cli_path"`
	VAD            bool   `yaml:"vad"`
	VADModelPath   string `yaml:"vad_model_path"`
	VADThreshold   string `yaml:"vad_threshold"`
}

type TUIConfig struct {
	PollInterval  int    `yaml:"poll_interval"`
	AutoSync      bool   `yaml:"auto_sync"`
	AutoReconnect bool   `yaml:"auto_reconnect"`
	Editor        string `yaml:"editor"`
}

type LLMConfig struct {
	Provider string `yaml:"provider"`
	APIKey   string `yaml:"api_key"`
	BaseURL  string `yaml:"base_url"`
	Model    string `yaml:"model"`
}

type Config struct {
	APIToken          string        `yaml:"api_token"`
	SDKAppKey         string        `yaml:"sdk_app_key"`
	SDKAppSecret      string        `yaml:"sdk_app_secret"`
	DeviceSN          string        `yaml:"device_sn"`
	SNSignatureB64    string        `yaml:"sn_signature_b64"`
	RSAPrivateKeyPEM  string        `yaml:"rsa_private_key_pem"`
	E2EERSKeyPath     string        `yaml:"e2ee_rsa_key_path"`
	SDKTokenCachePath string        `yaml:"sdk_token_cache_path"`
	DownloadDir       string        `yaml:"download_dir"`
	Whisper           WhisperConfig `yaml:"whisper"`
	BLEScanDuration   float64       `yaml:"ble_scan_duration"`
	TUI               TUIConfig     `yaml:"tui"`
	LLM               LLMConfig     `yaml:"llm"`
	MemoryDBPath      string        `yaml:"memory_db_path"`
	MemoryMaxAgeDays  int           `yaml:"memory_max_age_days"`

	rsaPrivateKey *rsa.PrivateKey
}

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		E2EERSKeyPath:     filepath.Join(home, ".config", "aplaud", "plaud_rsa_key"),
		SDKTokenCachePath: filepath.Join(home, ".config", "aplaud", "sdk_token_cache.json"),
		DownloadDir:       filepath.Join(home, "Recordings", "Plaud"),
		BLEScanDuration:   10.0,
		Whisper: WhisperConfig{
			Language:       "en",
			WhisperCliPath: "whisper-cli",
		},
		TUI: TUIConfig{
			PollInterval:  300,
			AutoSync:      true,
			AutoReconnect: true,
		},
		LLM: LLMConfig{
			Provider: "deepseek",
			BaseURL:  "https://api.deepseek.com",
			Model:    "deepseek-chat",
		},
		MemoryDBPath:     filepath.Join(home, ".config", "aplaud", "memory.db"),
		MemoryMaxAgeDays: 90,
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.expandPaths()

	return cfg, nil
}

func (c *Config) expandPaths() {
	home, _ := os.UserHomeDir()
	c.E2EERSKeyPath = expandHome(c.E2EERSKeyPath, home)
	c.SDKTokenCachePath = expandHome(c.SDKTokenCachePath, home)
	c.DownloadDir = expandHome(c.DownloadDir, home)
	c.Whisper.ModelPath = expandHome(c.Whisper.ModelPath, home)
	c.Whisper.LibPath = expandHome(c.Whisper.LibPath, home)
	c.Whisper.GGMLLibPath = expandHome(c.Whisper.GGMLLibPath, home)
	c.Whisper.WhisperCliPath = expandHome(c.Whisper.WhisperCliPath, home)
	c.Whisper.VADModelPath = expandHome(c.Whisper.VADModelPath, home)
	c.MemoryDBPath = expandHome(c.MemoryDBPath, home)
}

func expandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func (c *Config) GetRSAPrivateKey() (*rsa.PrivateKey, error) {
	if c.rsaPrivateKey != nil {
		return c.rsaPrivateKey, nil
	}
	if c.RSAPrivateKeyPEM == "" {
		return nil, fmt.Errorf("rsa_private_key_pem not configured")
	}
	key, err := crypto.LoadRSAPrivateKey(c.RSAPrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading RSA private key: %w", err)
	}
	c.rsaPrivateKey = key
	return key, nil
}

func (c *Config) GetE2EERSAPrivateKey() (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(c.E2EERSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading E2EE RSA key from %s: %w", c.E2EERSKeyPath, err)
	}
	return crypto.LoadRSAPrivateKey(string(data))
}

func (c *Config) GetSNSignatureBytes() ([]byte, error) {
	if c.SNSignatureB64 == "" {
		return nil, fmt.Errorf("sn_signature_b64 not configured")
	}
	return base64.StdEncoding.DecodeString(c.SNSignatureB64)
}

func (c *Config) GetJWTSubject() (string, error) {
	if c.APIToken == "" {
		return "", fmt.Errorf("api_token not configured")
	}
	parts := strings.Split(c.APIToken, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid JWT format")
	}
	payload := parts[1]
	for len(payload)%4 != 0 {
		payload += "="
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decoding JWT payload: %w", err)
	}

	type jwtClaims struct {
		Sub string `json:"sub"`
	}
	var claims jwtClaims
	if err := yaml.Unmarshal(decoded, &claims); err != nil {
		return "", fmt.Errorf("parsing JWT claims: %w", err)
	}
	if claims.Sub == "" {
		return "", fmt.Errorf("JWT has no sub claim")
	}
	return claims.Sub, nil
}

func (c *LLMConfig) EffectiveBaseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	switch c.Provider {
	case "deepseek":
		return "https://api.deepseek.com"
	case "openai":
		return "https://api.openai.com"
	default:
		return "https://api.deepseek.com"
	}
}

func (c *LLMConfig) EffectiveModel() string {
	if c.Model != "" {
		return c.Model
	}
	switch c.Provider {
	case "deepseek":
		return "deepseek-chat"
	case "openai":
		return "gpt-4o-mini"
	default:
		return "deepseek-chat"
	}
}

func (c *LLMConfig) Configured() bool {
	return c.APIKey != ""
}

func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "aplaud", "config.yaml")
}
