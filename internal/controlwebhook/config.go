package controlwebhook

import (
	"errors"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	envURL              = "GOPHERAI_CONTROL_WEBHOOK_URL"
	envSecretFile       = "GOPHERAI_CONTROL_WEBHOOK_SECRET_FILE"
	envLoopbackReceiver = "GOPHERAI_CONTROL_WEBHOOK_LOOPBACK_RECEIVER"
)

type Config struct {
	Enabled          bool
	Endpoint         *url.URL
	Secret           []byte
	EndpointMode     string
	LoopbackReceiver bool
	RequestTimeout   time.Duration
	PollInterval     time.Duration
	LeaseDuration    time.Duration
	MaxAttempts      int
}

func ConfigFromEnvironment() (Config, error) {
	rawURL := strings.TrimSpace(os.Getenv(envURL))
	secretFile := strings.TrimSpace(os.Getenv(envSecretFile))
	loopback := strings.EqualFold(strings.TrimSpace(os.Getenv(envLoopbackReceiver)), "true")
	if rawURL == "" && secretFile == "" {
		return Config{Enabled: false, EndpointMode: "disabled", RequestTimeout: 3 * time.Second, PollInterval: time.Second, LeaseDuration: 10 * time.Second, MaxAttempts: 3}, nil
	}
	if rawURL == "" || secretFile == "" {
		return Config{}, errors.New("webhook URL and secret file must be configured together")
	}
	endpoint, err := url.Parse(rawURL)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return Config{}, errors.New("webhook endpoint is invalid")
	}
	isLoopback := endpoint.Hostname() == "localhost" || net.ParseIP(endpoint.Hostname()) != nil && net.ParseIP(endpoint.Hostname()).IsLoopback()
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopback && isLoopback) {
		return Config{}, errors.New("webhook endpoint must use HTTPS unless the staging loopback receiver is explicitly enabled")
	}
	secretInfo, err := os.Stat(secretFile)
	if err != nil || !secretInfo.Mode().IsRegular() || secretInfo.Size() < 32 || secretInfo.Size() > 512 {
		return Config{}, errors.New("webhook secret file is invalid")
	}
	if runtime.GOOS != "windows" && secretInfo.Mode().Perm()&0o077 != 0 {
		return Config{}, errors.New("webhook secret file permissions are too broad")
	}
	secret, err := os.ReadFile(secretFile)
	if err != nil {
		return Config{}, errors.New("webhook secret file cannot be read")
	}
	secret = []byte(strings.TrimSpace(string(secret)))
	if len(secret) < 32 || len(secret) > 256 {
		return Config{}, errors.New("webhook secret length is invalid")
	}
	mode := "external_https"
	if isLoopback {
		mode = "staging_loopback"
	}
	return Config{
		Enabled: true, Endpoint: endpoint, Secret: secret, EndpointMode: mode, LoopbackReceiver: loopback,
		RequestTimeout: 3 * time.Second, PollInterval: time.Second, LeaseDuration: 10 * time.Second, MaxAttempts: 3,
	}, nil
}

var (
	defaultConfigOnce sync.Once
	defaultConfig     Config
	defaultConfigErr  error
)

func DefaultConfig() (Config, error) {
	defaultConfigOnce.Do(func() { defaultConfig, defaultConfigErr = ConfigFromEnvironment() })
	return defaultConfig, defaultConfigErr
}
