package account

import (
	"strings"
	"testing"
	"time"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	p := NewPool(dir, []string{"default", "b", "c"})

	if cur, ok := p.Current(now); !ok || cur != "default" {
		t.Fatalf("initial current = %q, %v", cur, ok)
	}
	p.MarkExhausted("default", now.Add(time.Hour))
	if cur, ok := p.Current(now); !ok || cur != "b" {
		t.Fatalf("after default limited: %q, %v", cur, ok)
	}

	// State survives a restart.
	p = NewPool(dir, []string{"default", "b", "c"})
	if cur, _ := p.Current(now); cur != "b" {
		t.Fatalf("reloaded current = %q", cur)
	}

	p.MarkExhausted("b", now.Add(3*time.Hour))
	p.MarkExhausted("c", now.Add(2*time.Hour))
	cur, ok := p.Current(now)
	if ok || cur != "default" {
		t.Fatalf("all limited: want default (resets first), false; got %q, %v", cur, ok)
	}
	if got := p.NextReset(now); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("NextReset = %v", got)
	}

	// Once default's reset passes it is usable again.
	if cur, ok := p.Current(now.Add(90 * time.Minute)); !ok || cur != "default" {
		t.Fatalf("after reset: %q, %v", cur, ok)
	}
}

func TestEnv(t *testing.T) {
	p := NewPool(t.TempDir(), []string{"default", "b"})
	if env := p.Env("default"); env != nil {
		t.Fatalf("default env = %v", env)
	}
	env := p.Env("b")
	if len(env) != 1 || !strings.HasPrefix(env[0], SlotEnv+"=") || !strings.HasSuffix(env[0], "/accounts/b") {
		t.Fatalf("b env = %v", env)
	}
	if NewPool(t.TempDir(), nil) != nil {
		t.Fatal("empty account list must disable rotation")
	}
}
