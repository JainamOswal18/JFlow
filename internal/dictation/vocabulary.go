package dictation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// VocabularyStore keeps personal corrections in one small local JSON file.
// Writes are atomic so a service restart or power loss never leaves a partial
// vocabulary file behind.
type VocabularyStore struct {
	path string
	mu   sync.Mutex
}

func NewVocabularyStore(path string) *VocabularyStore {
	return &VocabularyStore{path: path}
}

func (s *VocabularyStore) List() ([]VocabularyEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *VocabularyStore) listLocked() ([]VocabularyEntry, error) {
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []VocabularyEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []VocabularyEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Heard) < strings.ToLower(entries[j].Heard)
	})
	return entries, nil
}

func (s *VocabularyStore) Add(heard, replacement string) (VocabularyEntry, error) {
	heard = strings.TrimSpace(heard)
	replacement = strings.TrimSpace(replacement)
	if heard == "" || replacement == "" {
		return VocabularyEntry{}, errors.New("both heard text and replacement are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.listLocked()
	if err != nil {
		return VocabularyEntry{}, err
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Heard, heard) {
			return VocabularyEntry{}, errors.New("a correction for that heard text already exists")
		}
	}
	id, err := newID()
	if err != nil {
		return VocabularyEntry{}, err
	}
	now := time.Now().UTC()
	entry := VocabularyEntry{ID: id, Heard: heard, Replacement: replacement, CreatedAt: now, UpdatedAt: now}
	entries = append(entries, entry)
	if err := s.saveLocked(entries); err != nil {
		return VocabularyEntry{}, err
	}
	return entry, nil
}

func (s *VocabularyStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.listLocked()
	if err != nil {
		return err
	}
	kept := entries[:0]
	found := false
	for _, entry := range entries {
		if entry.ID == id {
			found = true
			continue
		}
		kept = append(kept, entry)
	}
	if !found {
		return errors.New("vocabulary entry was not found")
	}
	return s.saveLocked(kept)
}

func (s *VocabularyStore) saveLocked(entries []VocabularyEntry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Apply replaces whole words or phrases, case-insensitively. Longest entries
// run first, which makes "JFlow app" win over a shorter "JFlow" entry.
func (s *VocabularyStore) Apply(text string) string {
	entries, err := s.List()
	if err != nil || strings.TrimSpace(text) == "" {
		return text
	}
	sort.SliceStable(entries, func(i, j int) bool { return len(entries[i].Heard) > len(entries[j].Heard) })
	for _, entry := range entries {
		pattern := regexp.MustCompile(`(?i)(^|[^[:alnum:]_])(` + regexp.QuoteMeta(entry.Heard) + `)($|[^[:alnum:]_])`)
		text = pattern.ReplaceAllStringFunc(text, func(match string) string {
			parts := pattern.FindStringSubmatch(match)
			return parts[1] + entry.Replacement + parts[3]
		})
	}
	return text
}
