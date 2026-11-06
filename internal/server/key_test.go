package server_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/secret"
)

// testKey is the secret the route tests' servers ask for and their requests carry, so that they
// test what a route does once the gate has let a request through (gate_test.go tests the gate).
func testKey(t *testing.T) secret.Secret {
	t.Helper()

	key, err := secret.Parse(strings.Repeat("A", 43))
	if err != nil {
		t.Fatal(err)
	}

	return key
}
