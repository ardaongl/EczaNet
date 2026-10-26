package platform

import (
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, e := ReadConfig(); e == nil {
		t.Fatal("missing DSN accepted")
	}
	t.Setenv("DATABASE_URL", "postgres://user:secret@db/demo")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("HTTP_TIMEOUT", "5s")
	t.Setenv("SHUTDOWN_TIMEOUT", "10s")
	t.Setenv("DB_POOL_MAX", "10")
	if _, e := ReadConfig(); e != nil {
		t.Fatal(e)
	}
	t.Setenv("ALLOWED_ORIGINS", "https://example.invalid")
	if _, e := ReadConfig(); e == nil {
		t.Fatal("insecure TLS cookie accepted")
	}
	t.Setenv("COOKIE_SECURE", "true")
	if _, e := ReadConfig(); e != nil {
		t.Fatal(e)
	}
	t.Setenv("DATABASE_URL", "secret")
	_, e := ReadConfig()
	if e == nil || strings.Contains(e.Error(), "secret") {
		t.Fatal("invalid DSN error")
	}
}
