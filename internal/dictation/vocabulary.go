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
	for i := range entries {
		normalizeVocabularyEntry(&entries[i])
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Canonical) < strings.ToLower(entries[j].Canonical)
	})
	return entries, nil
}

// Add keeps the old heard-as API working while storing the replacement as the
// canonical term and the heard text as a local alias.
func (s *VocabularyStore) Add(heard, replacement string) (VocabularyEntry, error) {
	heard = strings.TrimSpace(heard)
	replacement = strings.TrimSpace(replacement)
	if heard == "" || replacement == "" {
		return VocabularyEntry{}, errors.New("both heard text and replacement are required")
	}
	return s.addCanonical(replacement, heard)
}

// AddCanonical stores a word or phrase exactly as the user wants it written.
// It is later used both as an exact local case correction and as a Scribe
// keyterm. The user never needs to provide a guessed mis-transcription.
func (s *VocabularyStore) AddCanonical(canonical string) (VocabularyEntry, error) {
	return s.addCanonical(canonical, "")
}

func (s *VocabularyStore) addCanonical(canonical, alias string) (VocabularyEntry, error) {
	canonical = strings.TrimSpace(canonical)
	alias = strings.TrimSpace(alias)
	if canonical == "" {
		return VocabularyEntry{}, errors.New("a word or phrase is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.listLocked()
	if err != nil {
		return VocabularyEntry{}, err
	}
	for i := range entries {
		if strings.EqualFold(entries[i].Canonical, canonical) {
			if addAlias(&entries[i], alias) {
				entries[i].UpdatedAt = time.Now().UTC()
				if err := s.saveLocked(entries); err != nil {
					return VocabularyEntry{}, err
				}
			}
			return entries[i], nil
		}
	}
	id, err := newID()
	if err != nil {
		return VocabularyEntry{}, err
	}
	now := time.Now().UTC()
	entry := VocabularyEntry{ID: id, Canonical: canonical, CreatedAt: now, UpdatedAt: now}
	addAlias(&entry, alias)
	entries = append(entries, entry)
	if err := s.saveLocked(entries); err != nil {
		return VocabularyEntry{}, err
	}
	return entry, nil
}

// LearnAlias records a correction observed in a transcript. It is local-only:
// the alias is never sent as a cloud keyterm.
func (s *VocabularyStore) LearnAlias(canonical, alias string) (VocabularyEntry, error) {
	canonical = strings.TrimSpace(canonical)
	alias = strings.TrimSpace(alias)
	if canonical == "" || alias == "" || strings.EqualFold(canonical, alias) {
		return VocabularyEntry{}, nil
	}
	return s.addCanonical(canonical, alias)
}

// Keyterms returns canonical terms suitable for Scribe. Aliases are excluded
// deliberately: they are personal local history, while cloud prompting should
// receive only the spelling the user actually wants.
func (s *VocabularyStore) Keyterms(limit int) ([]string, error) {
	entries, err := s.List()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	terms := make([]string, 0, len(entries))
	for _, entry := range entries {
		term := strings.TrimSpace(entry.Canonical)
		if term == "" || len([]rune(term)) >= 50 || len(strings.Fields(term)) > 5 {
			continue
		}
		key := strings.ToLower(term)
		if !seen[key] {
			seen[key] = true
			terms = append(terms, term)
		}
		if limit > 0 && len(terms) >= limit {
			break
		}
	}
	return terms, nil
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
	for i := range entries {
		normalizeVocabularyEntry(&entries[i])
		entries[i].Heard = ""
		entries[i].Replacement = ""
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

// Apply replaces canonical casing and learned aliases, case-insensitively.
// Longest phrases run first, which makes a full name win over a short name.
func (s *VocabularyStore) Apply(text string) string {
	entries, err := s.List()
	if err != nil || strings.TrimSpace(text) == "" {
		return text
	}
	type correction struct{ heard, canonical string }
	corrections := make([]correction, 0, len(entries)*2)
	for _, entry := range entries {
		if entry.Canonical == "" {
			continue
		}
		corrections = append(corrections, correction{entry.Canonical, entry.Canonical})
		for _, alias := range entry.Aliases {
			corrections = append(corrections, correction{alias, entry.Canonical})
		}
	}
	sort.SliceStable(corrections, func(i, j int) bool { return len(corrections[i].heard) > len(corrections[j].heard) })
	for _, correction := range corrections {
		text = replaceWholePhrase(text, correction.heard, correction.canonical)
	}
	return text
}

func normalizeVocabularyEntry(entry *VocabularyEntry) {
	entry.Canonical = strings.TrimSpace(entry.Canonical)
	if entry.Canonical == "" {
		entry.Canonical = strings.TrimSpace(entry.Replacement)
	}
	if entry.Canonical == "" {
		entry.Canonical = strings.TrimSpace(entry.Heard)
	}
	aliases := make([]string, 0, len(entry.Aliases)+1)
	for _, alias := range append(entry.Aliases, entry.Heard) {
		alias = strings.TrimSpace(alias)
		if alias == "" || strings.EqualFold(alias, entry.Canonical) {
			continue
		}
		duplicate := false
		for _, existing := range aliases {
			if strings.EqualFold(existing, alias) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			aliases = append(aliases, alias)
		}
	}
	entry.Aliases = aliases
}

func addAlias(entry *VocabularyEntry, alias string) bool {
	alias = strings.TrimSpace(alias)
	if alias == "" || strings.EqualFold(alias, entry.Canonical) {
		return false
	}
	for _, existing := range entry.Aliases {
		if strings.EqualFold(existing, alias) {
			return false
		}
	}
	entry.Aliases = append(entry.Aliases, alias)
	return true
}

// LearnFromCorrection extracts high-similarity changed phrases from a user's
// saved transcript edit. It deliberately learns only close spelling/spacing
// variants ("Jay Nam" -> "Jainam"), not semantic rewrites of a sentence.
func (s *VocabularyStore) LearnFromCorrection(raw, corrected string) (int, error) {
	rawWords := strings.Fields(raw)
	correctedWords := strings.Fields(corrected)
	if len(rawWords) == 0 || len(correctedWords) == 0 {
		return 0, nil
	}
	type candidate struct {
		alias string
		score float64
	}
	best := map[string]candidate{}
	for length := 1; length <= 3; length++ {
		for start := 0; start+length <= len(correctedWords); start++ {
			canonical := strings.Join(correctedWords[start:start+length], " ")
			foldedCanonical := compactVocabularyText(canonical)
			if len([]rune(foldedCanonical)) < 4 || containsWholePhrase(raw, canonical) {
				continue
			}
			for rawLength := 1; rawLength <= 3; rawLength++ {
				for rawStart := 0; rawStart+rawLength <= len(rawWords); rawStart++ {
					alias := strings.Join(rawWords[rawStart:rawStart+rawLength], " ")
					foldedAlias := compactVocabularyText(alias)
					if foldedAlias == "" || foldedAlias == foldedCanonical {
						continue
					}
					score := similarity(foldedCanonical, foldedAlias)
					if score < 0.80 {
						continue
					}
					current, exists := best[foldedCanonical]
					if !exists || score > current.score || (score == current.score && len(alias) < len(current.alias)) {
						best[foldedCanonical] = candidate{alias: alias, score: score}
					}
				}
			}
		}
	}
	learned := 0
	for length := 1; length <= 3; length++ {
		for start := 0; start+length <= len(correctedWords); start++ {
			canonical := strings.Join(correctedWords[start:start+length], " ")
			if candidate, ok := best[compactVocabularyText(canonical)]; ok {
				if _, err := s.LearnAlias(canonical, candidate.alias); err != nil {
					return learned, err
				}
				learned++
			}
		}
	}
	return learned, nil
}

func containsWholePhrase(text, phrase string) bool {
	return strings.Contains(" "+compactVocabularyText(text)+" ", " "+compactVocabularyText(phrase)+" ")
}

func compactVocabularyText(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func similarity(a, b string) float64 {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 || len(br) == 0 {
		return 0
	}
	prev := make([]int, len(br)+1)
	current := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, r := range ar {
		current[0] = i + 1
		for j, other := range br {
			cost := 0
			if r != other {
				cost = 1
			}
			current[j+1] = minInt(current[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev, current = current, prev
	}
	maxLen := len(ar)
	if len(br) > maxLen {
		maxLen = len(br)
	}
	return 1 - float64(prev[len(br)])/float64(maxLen)
}

func minInt(values ...int) int {
	min := values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
	}
	return min
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
