package agentruntime

import (
	"errors"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	registry := NewRegistry()
	mockRunner := NewMockRunner()

	if err := registry.Register("mock", mockRunner); err != nil {
		t.Fatalf("register mock runner: %v", err)
	}
	if err := registry.SetDefault("mock"); err != nil {
		t.Fatalf("set default provider: %v", err)
	}

	gotByName, err := registry.Get("mock")
	if err != nil {
		t.Fatalf("get runner by provider: %v", err)
	}
	if gotByName != mockRunner {
		t.Fatal("expected fetched provider runner to match registered runner")
	}

	gotDefault, err := registry.Get("")
	if err != nil {
		t.Fatalf("get default runner: %v", err)
	}
	if gotDefault != mockRunner {
		t.Fatal("expected fetched default runner to match registered runner")
	}

	if registry.DefaultProvider() != "mock" {
		t.Fatalf("expected default provider mock, got %s", registry.DefaultProvider())
	}
}

func TestRegistryGetReturnsUnregisteredError(t *testing.T) {
	registry := NewRegistry()

	if _, err := registry.Get("missing"); !errors.Is(err, ErrRunnerNotRegistered) {
		t.Fatalf("expected ErrRunnerNotRegistered, got %v", err)
	}

	if _, err := registry.Get(""); !errors.Is(err, ErrDefaultProviderNotSet) {
		t.Fatalf("expected ErrDefaultProviderNotSet, got %v", err)
	}
}
