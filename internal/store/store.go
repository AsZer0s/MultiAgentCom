package store

import (
	"context"
	"os"
	"path/filepath"
)

type Store interface {
	Load(ctx context.Context) ([]byte, error)
	Save(ctx context.Context, payload []byte) error
}

type MemoryStore struct{}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) Load(_ context.Context) ([]byte, error) {
	return nil, nil
}

func (s *MemoryStore) Save(_ context.Context, _ []byte) error {
	return nil
}

type FileStore struct {
	path string
}

func NewFileStore(root string) *FileStore {
	return &FileStore{path: filepath.Join(root, "service-state.json")}
}

func (s *FileStore) Load(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	payload, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *FileStore) Save(ctx context.Context, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	file, err := os.CreateTemp(dir, ".service-state-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)

	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}
