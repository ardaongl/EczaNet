package auth

import (
	"testing"
	"time"
)

func TestAbsoluteSessionBoundary(t *testing.T) {
	expires := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	s := Session{Expires: expires}
	if !Valid(s, expires.Add(-time.Nanosecond)) || Valid(s, expires) || Valid(s, expires.Add(time.Nanosecond)) {
		t.Fatal("8h absolute boundary")
	}
}
func TestDefaultDenyRole(t *testing.T) {
	for _, role := range []string{"simulator", "admin", "", "unknown"} {
		if Interactive(role) {
			t.Fatalf("unexpected access: %q", role)
		}
	}
	for _, role := range []string{"pharmacist", "technician", "warehouse", "manager", "accountant"} {
		if !Interactive(role) {
			t.Fatal(role)
		}
	}
}
