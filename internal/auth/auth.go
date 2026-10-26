package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}
type Location struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type Session struct {
	User     User
	CSRFHash string
	Expires  time.Time
}

type Store interface {
	Credentials(context.Context, string) (User, string, error)
	CreateSession(context.Context, string, string, string, time.Time) error
	Session(context.Context, string) (Session, error)
	Revoke(context.Context, string) error
	Locations(context.Context, string) ([]Location, error)
	Ping(context.Context) error
}

func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("entropy unavailable")
	}
	return hex.EncodeToString(b)
}
func Hash(s string) string                { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Valid(s Session, now time.Time) bool { return now.Before(s.Expires) }

// A1 exposes only identity/location reads. Service accounts get no interactive access.
func Interactive(role string) bool {
	switch role {
	case "pharmacist", "technician", "warehouse", "manager", "accountant":
		return true
	}
	return false
}
