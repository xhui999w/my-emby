package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Persister 抽象持久化后端。目前提供 JSON 文件与 PostgreSQL 两种实现。
type Persister interface {
	Load() (*dbSnapshot, error)
	Save(*dbSnapshot) error
	Close() error
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// ---------------- JSON 文件后端 ----------------

type jsonPersister struct {
	path string
	mu   sync.Mutex
}

func newJSONPersister(dir string) *jsonPersister {
	return &jsonPersister{path: filepath.Join(dir, "db.json")}
}

func (p *jsonPersister) Load() (*dbSnapshot, error) {
	raw, err := os.ReadFile(p.path)
	if err != nil {
		return nil, err
	}
	var snap dbSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (p *jsonPersister) Save(snap *dbSnapshot) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func (p *jsonPersister) Close() error { return nil }
