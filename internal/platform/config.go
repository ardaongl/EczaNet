package platform

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL, Address                    string
	Origins                                 map[string]bool
	CookieSecure                            bool
	RequestTimeout, ShutdownTimeout         time.Duration
	PoolMax                                 int32
	StockLockTimeout, StockStatementTimeout time.Duration
}

// ReadConfig never includes supplied values in errors: a DSN may contain a password.
func ReadConfig() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), Address: ":8080", Origins: map[string]bool{}}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL zorunlu")
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return c, errors.New("DATABASE_URL gecersiz")
	}
	if a := os.Getenv("HTTP_ADDR"); a != "" {
		c.Address = a
	}
	for _, origin := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		u, e := url.Parse(origin)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" {
			return c, errors.New("ALLOWED_ORIGINS zorunlu ve origin biciminde olmali")
		}
		c.Origins[origin] = true
	}
	c.CookieSecure, err = strconv.ParseBool(env("COOKIE_SECURE", "false"))
	if err != nil {
		return c, errors.New("COOKIE_SECURE gecersiz")
	}
	for origin := range c.Origins {
		if strings.HasPrefix(origin, "https://") && !c.CookieSecure {
			return c, errors.New("TLS origin icin COOKIE_SECURE=true gerekli")
		}
		if !strings.HasPrefix(origin, "https://") && !strings.HasPrefix(origin, "http://localhost:") && !c.CookieSecure {
			return c, errors.New("HTTP yalniz localhost demo icin; TLS ve Secure gerekli")
		}
	}
	c.RequestTimeout, err = time.ParseDuration(env("HTTP_TIMEOUT", "15s"))
	if err != nil || c.RequestTimeout <= 0 {
		return c, errors.New("HTTP_TIMEOUT gecersiz")
	}
	c.StockLockTimeout, err = time.ParseDuration(env("STOCK_LOCK_TIMEOUT", "2s"))
	if err != nil || c.StockLockTimeout < time.Millisecond {
		return c, errors.New("STOCK_LOCK_TIMEOUT gecersiz")
	}
	c.StockStatementTimeout, err = time.ParseDuration(env("STOCK_STATEMENT_TIMEOUT", "5s"))
	if err != nil || c.StockStatementTimeout < c.StockLockTimeout || c.StockStatementTimeout > c.RequestTimeout {
		return c, errors.New("STOCK_STATEMENT_TIMEOUT lock ve HTTP butcesi arasinda olmali")
	}
	c.ShutdownTimeout, err = time.ParseDuration(env("SHUTDOWN_TIMEOUT", "10s"))
	if err != nil || c.ShutdownTimeout <= 0 {
		return c, errors.New("SHUTDOWN_TIMEOUT gecersiz")
	}
	n, err := strconv.ParseInt(env("DB_POOL_MAX", "10"), 10, 32)
	if err != nil || n < 1 || n > 100 {
		return c, errors.New("DB_POOL_MAX 1..100 olmali")
	}
	c.PoolMax = int32(n)
	return c, nil
}
func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
