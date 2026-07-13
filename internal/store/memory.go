package store

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

// Device is a registered kumad machine (BYO or cloud-provisioned).
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	MachineID string    `json:"machine_id"`
	Key       string    `json:"-"` // never serialized by default
	Kind      Kind      `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

// CloudAgent links a Kuma cloud agent to a Fuse environment.
type CloudAgent struct {
	ID                string    `json:"id"`
	Name              string    `json:"name,omitempty"`
	DeviceID          string    `json:"device_id"`
	MachineID         string    `json:"machine_id"`
	Key               string    `json:"-"`
	RelayURL          string    `json:"relay_url"`
	FuseEnvironmentID string    `json:"fuse_environment_id"`
	FuseState         string    `json:"fuse_state"`
	FuseURL           string    `json:"fuse_url,omitempty"`
	FuseError         string    `json:"fuse_error,omitempty"`
	CPUs              int32     `json:"cpus"`
	RamMB             int32     `json:"ram_mb"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Memory is a process-local store.
type Memory struct {
	mu          sync.RWMutex
	devices     map[string]Device
	byMachine   map[string]string
	cloudAgents map[string]CloudAgent
}

func NewMemory() *Memory {
	return &Memory{
		devices:     make(map[string]Device),
		byMachine:   make(map[string]string),
		cloudAgents: make(map[string]CloudAgent),
	}
}

func (m *Memory) CreateDevice(d Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devices[d.ID]; ok {
		return ErrAlreadyExists
	}
	if _, ok := m.byMachine[d.MachineID]; ok {
		return ErrAlreadyExists
	}
	m.devices[d.ID] = d
	m.byMachine[d.MachineID] = d.ID
	return nil
}

func (m *Memory) GetDevice(id string) (Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	if !ok {
		return Device{}, ErrNotFound
	}
	return d, nil
}

func (m *Memory) ListDevices() []Device {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Device, 0, len(m.devices))
	for _, d := range m.devices {
		out = append(out, d)
	}
	return out
}

func (m *Memory) DeleteDevice(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.devices, id)
	delete(m.byMachine, d.MachineID)
	return nil
}

func (m *Memory) CreateCloudAgent(a CloudAgent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cloudAgents[a.ID]; ok {
		return ErrAlreadyExists
	}
	m.cloudAgents[a.ID] = a
	return nil
}

func (m *Memory) GetCloudAgent(id string) (CloudAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.cloudAgents[id]
	if !ok {
		return CloudAgent{}, ErrNotFound
	}
	return a, nil
}

func (m *Memory) ListCloudAgents() []CloudAgent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CloudAgent, 0, len(m.cloudAgents))
	for _, a := range m.cloudAgents {
		out = append(out, a)
	}
	return out
}

func (m *Memory) UpdateCloudAgent(a CloudAgent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cloudAgents[a.ID]; !ok {
		return ErrNotFound
	}
	m.cloudAgents[a.ID] = a
	return nil
}

func (m *Memory) DeleteCloudAgent(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cloudAgents[id]; !ok {
		return ErrNotFound
	}
	delete(m.cloudAgents, id)
	return nil
}
