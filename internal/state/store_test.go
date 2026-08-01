package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/abhyuday404/prava-hack/internal/domain"
)

func TestStoreRoundTrip(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), ".prava-deploy", "state.json"))
	plan := domain.Plan{ID: "plan_test", CreatedAt: time.Now().UTC()}
	if err := store.Update(func(j *domain.Journal) error { j.Plans[plan.ID] = plan; return nil }); err != nil {
		t.Fatal(err)
	}
	journal, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if journal.Schema != domain.SchemaVersion || journal.Plans[plan.ID].ID != plan.ID {
		t.Fatalf("unexpected journal: %#v", journal)
	}
}

func TestStoreRejectsUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := New(path)
	if err := store.Update(func(j *domain.Journal) error { j.Schema = "v999"; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected unsupported schema error")
	}
}
