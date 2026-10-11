package cursor

import "testing"

func TestConfigDefaults(t *testing.T) {
	c, err := ParseConfig(nil)
	if err != nil || c != DefaultConfig() {
		t.Fatalf("defaults: %#v %v", c, err)
	}
	c, err = ParseConfig([]byte("enabled: false\nrequest-timeout: 42\n"))
	if err != nil || c.Enabled || c.RequestTimeout != 42 {
		t.Fatalf("decode: %#v %v", c, err)
	}
}

func TestConfigRejectsUnsafeOrMalformed(t *testing.T) {
	for _, raw := range []string{
		"unknown: true", "enabled: not-bool", "enabled: true\nenabled: false", "---\nenabled: true\n---\nenabled: false",
		"api-base-url: http://example.com", "api-base-url: https://user:secret@example.com", "api-base-url: https://example.com?q=secret", "api-base-url: https://example.com?", "login-url: https://example.com/#secret", "request-timeout: 0", "login-timeout: -1", "max-concurrent-streams: 2048", "client-version: ''",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseConfig([]byte(raw)); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestLoopbackEndpoints(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080", "https://example.com/path"} {
		if err := validateURL(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
