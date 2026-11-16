package database

import (
	"context"
	"testing"
)

func TestHostScopeIsACopy(t *testing.T) {
	groups := []string{"a", "b"}
	ctx := WithHostScope(context.Background(), groups)
	groups[0] = "z"
	got, limited := HostScope(ctx)
	if !limited || len(got) != 2 || got[0] != "a" {
		t.Fatalf("got %v %v", got, limited)
	}
	if _, limited := HostScope(context.Background()); limited {
		t.Fatal("a plain context is limited")
	}
	// No group at all is still limited: it sees nothing, not everything.
	if got, limited := HostScope(WithHostScope(context.Background(), nil)); !limited || len(got) != 0 {
		t.Fatalf("empty scope: %v %v", got, limited)
	}
}

// A connection never used limited is handed out untouched.
func TestUnlimitedUseOfACleanConnectionCostsNothing(t *testing.T) {
	ok, err := prepareHostScope(context.Background(), nil)
	if !ok || err != nil {
		t.Fatalf("got %v %v", ok, err)
	}
}

// Group ids travel joined by commas: one containing a comma, or an empty one,
// is refused before anything reaches the connection.
func TestMalformedGroupIdsAreRefused(t *testing.T) {
	for _, groups := range [][]string{{"a,b"}, {""}} {
		ok, err := prepareHostScope(WithHostScope(context.Background(), groups), nil)
		if ok || err == nil {
			t.Errorf("%q: got %v %v", groups, ok, err)
		}
	}
}
