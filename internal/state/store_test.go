package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abhyuday404/prava-hack/internal/domain"
)

func TestStoreRoundTrip(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), ".warden", "state.json"))
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

func TestProjectStoreReadsLegacyStateAndMigratesOnWrite(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, ".prava-deploy", "state.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := emptyJournal()
	legacy.Plans["plan_legacy"] = domain.Plan{ID: "plan_legacy", CreatedAt: time.Now().UTC()}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewProject(root)
	journal, err := store.Load()
	if err != nil || journal.Plans["plan_legacy"].ID == "" {
		t.Fatalf("load legacy state: journal=%#v err=%v", journal, err)
	}
	if err := store.Update(func(j *domain.Journal) error {
		j.Plans["plan_new"] = domain.Plan{ID: "plan_new", CreatedAt: time.Now().UTC()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(PathFor(root)); err != nil {
		t.Fatalf("expected migrated Warden state: %v", err)
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
