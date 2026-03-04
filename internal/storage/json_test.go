package storage

import (
	"os"
	"testing"
)

func TestPersistenceAcrossReload(t *testing.T) {
	f, err := os.CreateTemp("", "storage_test_*.json")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(name) })

	s1, err := NewJSONStorage(name)
	if err != nil {
		t.Fatal(err)
	}

	cfg := ChatConfig{ChatID: 123, NotifyTime: "09:00", Timezone: "UTC"}
	if err := s1.Set(cfg); err != nil {
		t.Fatal(err)
	}

	// Reload from disk
	s2, err := NewJSONStorage(name)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := s2.Get(123)
	if !ok {
		t.Fatal("config should survive a reload")
	}
	if got.NotifyTime != "09:00" {
		t.Errorf("NotifyTime: got %q, want %q", got.NotifyTime, "09:00")
	}
	if got.Timezone != "UTC" {
		t.Errorf("Timezone: got %q, want %q", got.Timezone, "UTC")
	}
}
