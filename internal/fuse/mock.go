package fuse

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Mock is an in-memory Fuse client for tests.
type Mock struct {
	mu   sync.Mutex
	envs map[string]*Environment
	fail CreateFailFunc
}

// CreateFailFunc optionally fails CreateEnvironment in tests.
type CreateFailFunc func(CreateParams) error

// NewMock returns an empty mock Fuse client.
func NewMock() *Mock {
	return &Mock{envs: make(map[string]*Environment)}
}

// SetCreateFail configures CreateEnvironment to fail when fn returns an error.
func (m *Mock) SetCreateFail(fn CreateFailFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = fn
}

func (m *Mock) CreateEnvironment(_ context.Context, params CreateParams) (*Environment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		if err := m.fail(params); err != nil {
			return nil, err
		}
	}
	id := "fuse_" + params.TaskID
	if _, ok := m.envs[id]; ok {
		return nil, fmt.Errorf("already exists")
	}
	now := time.Now().UTC()
	env := &Environment{
		ID:        id,
		TaskID:    params.TaskID,
		State:     "provisioning",
		URL:       "http://vm.local:3000",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.envs[id] = env
	cp := *env
	return &cp, nil
}

func (m *Mock) GetEnvironment(_ context.Context, id string) (*Environment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	env, ok := m.envs[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	if env.State == "provisioning" {
		env.State = "running"
		env.UpdatedAt = time.Now().UTC()
	}
	cp := *env
	return &cp, nil
}

func (m *Mock) ListEnvironments(_ context.Context) ([]Environment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Environment, 0, len(m.envs))
	for _, env := range m.envs {
		out = append(out, *env)
	}
	return out, nil
}

func (m *Mock) DestroyEnvironment(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.envs[id]; !ok {
		return fmt.Errorf("not found")
	}
	delete(m.envs, id)
	return nil
}

// Envs returns a copy of the mock environment map.
func (m *Mock) Envs() map[string]*Environment {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]*Environment, len(m.envs))
	for k, v := range m.envs {
		cp := *v
		out[k] = &cp
	}
	return out
}
