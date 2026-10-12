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

func TestConfigAcceptsHostInjectedPriority(t *testing.T) {
	for _, raw := range []string{"enabled: true\npriority: 0\n", "enabled: true\npriority: -5\n"} {
		c, err := ParseConfig([]byte(raw))
		if err != nil || !c.Enabled {
			t.Fatalf("%q rejected: %#v %v", raw, c, err)
		}
	}
	if _, err := ParseConfig([]byte("priority: high\n")); err == nil {
		t.Fatal("non-integer priority accepted")
	}
}

func TestConfigRejectsUnsafeOrMalformed(t *testing.T) {
	for _, raw := range []string{
		"unknown: true", "enabled: not-bool", "enabled: true\nenabled: false", "---\nenabled: true\n---\nenabled: false",
		"api-base-url: http://example.com", "api-base-url: https://user:secret@example.com", "api-base-url: https://example.com?q=secret", "api-base-url: https://example.com?", "login-url: https://example.com/#secret", "request-timeout: 0", "login-timeout: -1", "max-concurrent-streams: 2048", "execution-timeout: 0", "execution-timeout: 3601", "client-version: ''", "model-prefix: ''", "model-prefix: a/b", "model-prefix: 'has space'",
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

func TestConfigModelPrefix(t *testing.T) {
	if DefaultConfig().ModelPrefix != "cursor" {
		t.Fatalf("default prefix = %q", DefaultConfig().ModelPrefix)
	}
	c, err := ParseConfig([]byte("model-prefix: work_1.x\n"))
	if err != nil || c.ModelPrefix != "work_1.x" {
		t.Fatalf("custom prefix: %#v %v", c, err)
	}
	if (Config{}).WithDefaults().ModelPrefix != "cursor" {
		t.Fatal("WithDefaults left the prefix empty")
	}
}
