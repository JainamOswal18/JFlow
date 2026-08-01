package dictation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}
func (s *Store) jobDir(id string) string    { return filepath.Join(s.dir, id) }
func (s *Store) jobPath(id string) string   { return filepath.Join(s.jobDir(id), "job.json") }
func (s *Store) AudioPath(id string) string { return filepath.Join(s.jobDir(id), "audio.wav") }

func (s *Store) Save(j *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(j)
}
func (s *Store) saveLocked(j *Job) error {
	j.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(s.jobDir(j.ID), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.jobPath(j.ID) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.jobPath(j.ID))
}
func (s *Store) Get(id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.jobPath(id))
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("read job %s: %w", id, err)
	}
	return &j, nil
}
func (s *Store) List() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	jobs := make([]*Job, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "job.json"))
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) == nil {
			jobs = append(jobs, &j)
		}
	}
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].CreatedAt.After(jobs[k].CreatedAt) })
	return jobs, nil
}
func (s *Store) DueJobs(now time.Time) ([]*Job, error) {
	jobs, err := s.List()
	if err != nil {
		return nil, err
	}
	var due []*Job
	for _, j := range jobs {
		if j.Status == StatusQueued || (j.Status == StatusRetryWait && !j.NextAttemptAt.After(now)) {
			due = append(due, j)
		}
	}
	return due, nil
}
func (s *Store) Purge(before time.Time, keepFailed bool) error {
	jobs, err := s.List()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.UpdatedAt.After(before) || (keepFailed && j.Status == StatusFailed) {
			continue
		}
		if err := os.RemoveAll(s.jobDir(j.ID)); err != nil {
			return err
		}
	}
	return nil
}
