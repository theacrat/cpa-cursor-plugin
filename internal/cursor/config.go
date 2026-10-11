package cursor

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultClientVersion is the official cursor.com/install release observed on
// 2026-10-11. It is a wire compatibility hint, not an installed CLI dependency.
const DefaultClientVersion = "cli-2026.10.01-e373342"

type Config struct {
	Enabled              bool   `yaml:"enabled"`
	APIBaseURL           string `yaml:"api-base-url"`
	LoginURL             string `yaml:"login-url"`
	AgentBaseURL         string `yaml:"agent-base-url"`
	ClientVersion        string `yaml:"client-version"`
	RequestTimeout       int    `yaml:"request-timeout"`
	LoginTimeout         int    `yaml:"login-timeout"`
	MaxConcurrentStreams int    `yaml:"max-concurrent-streams"`
}

func DefaultConfig() Config {
	return Config{Enabled: true, APIBaseURL: "https://api2.cursor.sh", LoginURL: "https://cursor.com/loginDeepControl", AgentBaseURL: "https://agentn.us.api5.cursor.sh", ClientVersion: DefaultClientVersion, RequestTimeout: 30, LoginTimeout: 300, MaxConcurrentStreams: 32}
}

// WithDefaults fills optional zero values without changing Enabled.
func (c Config) WithDefaults() Config {
	d := DefaultConfig()
	if c.APIBaseURL == "" {
		c.APIBaseURL = d.APIBaseURL
	}
	if c.LoginURL == "" {
		c.LoginURL = d.LoginURL
	}
	if c.AgentBaseURL == "" {
		c.AgentBaseURL = d.AgentBaseURL
	}
	if c.ClientVersion == "" {
		c.ClientVersion = d.ClientVersion
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = d.RequestTimeout
	}
	if c.LoginTimeout == 0 {
		c.LoginTimeout = d.LoginTimeout
	}
	if c.MaxConcurrentStreams == 0 {
		c.MaxConcurrentStreams = d.MaxConcurrentStreams
	}
	return c
}

func ParseConfig(data []byte) (Config, error) {
	c := DefaultConfig()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && err != io.EOF {
		return Config{}, errors.New("invalid cursor configuration")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("cursor configuration must contain one YAML document")
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) Validate() error {
	for _, raw := range []string{c.APIBaseURL, c.LoginURL, c.AgentBaseURL} {
		if err := validateURL(raw); err != nil {
			return err
		}
	}
	if c.RequestTimeout < 1 || c.RequestTimeout > 3600 || c.LoginTimeout < 1 || c.LoginTimeout > 3600 || c.MaxConcurrentStreams < 1 || c.MaxConcurrentStreams > 1024 {
		return errors.New("cursor configuration limits are out of range")
	}
	if strings.TrimSpace(c.ClientVersion) == "" || strings.ContainsAny(c.ClientVersion, "\r\n") || len(c.ClientVersion) > 128 {
		return errors.New("invalid cursor client version")
	}
	return nil
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("invalid cursor endpoint URL")
	}
	loopback := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return errors.New("cursor endpoints require HTTPS (except loopback)")
	}
	return nil
}
