package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreLoadMissingState(t *testing.T) {
	payload, err := NewFileStore(t.TempDir()).Load(context.Background())
	if err != nil {
		t.Fatalf("load missing state: %v", err)
	}
	if payload != nil {
		t.Fatalf("expected nil payload for missing state, got %q", payload)
	}
}

func TestFileStoreSaveCreatesStateFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "data")
	state := []byte(`{"version":1}`)

	if err := NewFileStore(root).Save(context.Background(), state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	payload, err := os.ReadFile(ServiceStatePath(root))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if string(payload) != string(state) {
		t.Fatalf("state file = %q, want %q", payload, state)
	}
}

func TestFileStoreSaveRejectsFileDataRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data-root")
	if err := os.WriteFile(root, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write file data root: %v", err)
	}

	err := NewFileStore(root).Save(context.Background(), []byte(`{"version":1}`))
	if err == nil {
		t.Fatal("expected save to fail when data root is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") && !strings.Contains(err.Error(), "not a dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceStatePath(t *testing.T) {
	root := t.TempDir()
	if got := ServiceStatePath(root); got != filepath.Join(root, ServiceStateFilename) {
		t.Fatalf("ServiceStatePath = %q", got)
	}
}
