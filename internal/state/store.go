package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/abhyuday404/Warden/internal/domain"
)

type Store struct {
	path       string
	legacyPath string
	mu         sync.Mutex
}

func New(path string) *Store { return &Store{path: path} }

func PathFor(projectRoot string) string {
	return filepath.Join(projectRoot, ".warden", "state.json")
}

func NewProject(projectRoot string) *Store {
	return &Store{
		path:       PathFor(projectRoot),
		legacyPath: filepath.Join(projectRoot, ".prava-deploy", "state.json"),
	}
}

func emptyJournal() domain.Journal {
	return domain.Journal{
		Schema:         domain.SchemaVersion,
		Plans:          map[string]domain.Plan{},
		Authorizations: map[string]domain.BudgetAuthorization{},
		Deployments:    map[string]domain.Deployment{},
	}
}

func (s *Store) Load() (domain.Journal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked()
}

func (s *Store) loadUnlocked() (domain.Journal, error) {
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) && s.legacyPath != "" {
		b, err = os.ReadFile(s.legacyPath)
	}
	if os.IsNotExist(err) {
		return emptyJournal(), nil
	}
	if err != nil {
		return domain.Journal{}, fmt.Errorf("read state: %w", err)
	}
	var journal domain.Journal
	if err := json.Unmarshal(b, &journal); err != nil {
		return domain.Journal{}, fmt.Errorf("decode state: %w", err)
	}
	if journal.Schema != domain.SchemaVersion {
		return domain.Journal{}, fmt.Errorf("unsupported state schema %q", journal.Schema)
	}
	if journal.Plans == nil {
		journal.Plans = map[string]domain.Plan{}
	}
	if journal.Authorizations == nil {
		journal.Authorizations = map[string]domain.BudgetAuthorization{}
	}
	if journal.Deployments == nil {
		journal.Deployments = map[string]domain.Deployment{}
	}
	return journal, nil
}

func (s *Store) Update(fn func(*domain.Journal) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	journal, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if err := fn(&journal); err != nil {
		return err
	}
	return s.saveUnlocked(journal)
}

func (s *Store) saveUnlocked(journal domain.Journal) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	b, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure state: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		// Windows cannot replace an existing path atomically. The state is non-secret
		// operational metadata, so fall back to remove-and-rename after the temp file is durable.
		if removeErr := os.Remove(s.path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("replace state: %w", err)
		}
		if renameErr := os.Rename(tmpName, s.path); renameErr != nil {
			return fmt.Errorf("replace state: %w", renameErr)
		}
	}
	return nil
}
