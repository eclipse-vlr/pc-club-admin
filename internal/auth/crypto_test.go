package auth

import (
	"strings"
	"testing"
)

func TestPINHashBoundary(t *testing.T) {
	secrets := Secrets{Pepper: "synthetic-test-pepper-outside-database"}
	a, b := secrets.Hash("123456", true), secrets.Hash("123456", true)
	if a == b || strings.Contains(a, "123456") {
		t.Fatal("hash must be independently salted")
	}
	if !secrets.Verify(a, "123456", true) || secrets.Verify(a, "123457", true) {
		t.Fatal("PIN verification incorrect")
	}
	if (Secrets{Pepper: "different-pepper"}).Verify(a, "123456", true) {
		t.Fatal("server pepper not enforced")
	}
	if secrets.Verify(strings.Replace(a, "m=65536", "m=999999999999", 1), "123456", true) {
		t.Fatal("unbounded KDF accepted")
	}
}
func TestInputBoundaries(t *testing.T) {
	if !ValidPIN("012345") || ValidPIN("１２３４５６") || ValidPIN("12345 ") {
		t.Fatal("PIN must be six ASCII digits")
	}
	if ValidPassword("short") || !ValidPassword("long-test-password") {
		t.Fatal("password policy incorrect")
	}
	if !ValidID("12345678-1234-1234-1234-123456789abc") || ValidID("not-an-id") {
		t.Fatal("invalid identifier validation")
	}
}
