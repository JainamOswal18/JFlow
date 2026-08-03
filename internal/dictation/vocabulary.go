package dictation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// VocabularyStore keeps personal corrections in one small local JSON file.
// Writes are atomic so a service restart or power loss never leaves a partial
// vocabulary file behind.
type VocabularyStore struct {
	path string
	mu   sync.Mutex
}

// NewVocabularyStore creates a local store at path.
func NewVocabularyStore(path string) *VocabularyStore {
	return &VocabularyStore{path: path}
}

// List returns all entries in a stable, human-friendly order.
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

// Add stores one case-insensitive heard-as correction.
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

// Delete removes one vocabulary entry by its local identifier.
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
		text = replaceWholePhrase(text, entry.Heard, entry.Replacement)
	}
	return text
}

func replaceWholePhrase(text, heard, replacement string) string {
	foldedText := strings.ToLower(text)
	foldedHeard := strings.ToLower(heard)
	if foldedHeard == "" {
		return text
	}
	var out strings.Builder
	searchFrom, copiedThrough := 0, 0
	for searchFrom < len(foldedText) {
		next := strings.Index(foldedText[searchFrom:], foldedHeard)
		if next < 0 {
			break
		}
		start := searchFrom + next
		end := start + len(foldedHeard)
		if isWordBoundaryBefore(text, start) && isWordBoundaryAfter(text, end) {
			out.WriteString(text[copiedThrough:start])
			out.WriteString(replacement)
			copiedThrough = end
		}
		// Advance past the candidate so repeated phrases separated by one space
		// or punctuation remain independently eligible for replacement.
		searchFrom = end
	}
	if copiedThrough == 0 {
		return text
	}
	out.WriteString(text[copiedThrough:])
	return out.String()
}

func isWordBoundaryBefore(text string, index int) bool {
	if index == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:index])
	return !isWordRune(r)
}

func isWordBoundaryAfter(text string, index int) bool {
	if index == len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[index:])
	return !isWordRune(r)
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}
