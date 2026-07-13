package store_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"kuma/internal/store"
)

func TestDeviceCRUD(t *testing.T) {
	m := store.NewMemory()
	d := store.Device{
		ID:        "dev_1",
		Name:      "mac",
		MachineID: "m_1",
		Key:       "secret",
		Kind:      store.KindDevice,
		CreatedAt: time.Now().UTC(),
	}
	if err := m.CreateDevice(d); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateDevice(d); err != store.ErrAlreadyExists {
		t.Fatalf("dup id: %v", err)
	}
	if err := m.CreateDevice(store.Device{ID: "dev_2", MachineID: "m_1", Kind: store.KindDevice}); err != store.ErrAlreadyExists {
		t.Fatalf("dup machine: %v", err)
	}

	got, err := m.GetDevice("dev_1")
	if err != nil || got.Name != "mac" || got.Key != "secret" {
		t.Fatalf("GetDevice: %+v err=%v", got, err)
	}
	list := m.ListDevices()
	if len(list) != 1 {
		t.Fatalf("list len=%d", len(list))
	}
	if err := m.DeleteDevice("dev_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetDevice("dev_1"); err != store.ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := m.DeleteDevice("missing"); err != store.ErrNotFound {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestCloudAgentCRUD(t *testing.T) {
	m := store.NewMemory()
	a := store.CloudAgent{
		ID:        "ca_1",
		Name:      "sandbox",
		DeviceID:  "dev_1",
		MachineID: "m_1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := m.CreateCloudAgent(a); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateCloudAgent(a); err != store.ErrAlreadyExists {
		t.Fatalf("dup: %v", err)
	}
	got, err := m.GetCloudAgent("ca_1")
	if err != nil || got.Name != "sandbox" {
		t.Fatalf("GetCloudAgent: %+v err=%v", got, err)
	}
	got.FuseState = "running"
	if err := m.UpdateCloudAgent(got); err != nil {
		t.Fatal(err)
	}
	got2, _ := m.GetCloudAgent("ca_1")
	if got2.FuseState != "running" {
		t.Fatalf("state=%q", got2.FuseState)
	}
	if err := m.UpdateCloudAgent(store.CloudAgent{ID: "missing"}); err != store.ErrNotFound {
		t.Fatalf("update missing: %v", err)
	}
	list := m.ListCloudAgents()
	if len(list) != 1 {
		t.Fatalf("list len=%d", len(list))
	}
	if err := m.DeleteCloudAgent("ca_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetCloudAgent("ca_1"); err != store.ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestMemoryConcurrentAccess(t *testing.T) {
	m := store.NewMemory()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := strconv.Itoa(i)
			_ = m.CreateDevice(store.Device{
				ID:        "dev_" + id,
				MachineID: "m_" + id,
				Kind:      store.KindDevice,
				CreatedAt: time.Now().UTC(),
			})
			_, _ = m.GetDevice("dev_" + id)
			_ = m.ListDevices()
		}(i)
	}
	wg.Wait()
}

// Ensure Memory implements Store.
var _ store.Store = (*store.Memory)(nil)
