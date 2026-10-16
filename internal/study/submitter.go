package study

import (
	"fmt"
	"sync"

	"github.com/3b391433/shanbay-cli/internal/api"
)

// Submitter serializes background writes. A failed write stops later writes:
// continuing from an uncertain server state could overwrite learning progress.
type Submitter struct {
	mu     sync.Mutex
	tail   chan struct{}
	err    error
	submit func(*Session, map[string]Grade) error
}

func NewSubmitter(c *api.Client, mbid string) *Submitter {
	client := c.WithWait(nil)
	return &Submitter{submit: func(s *Session, grades map[string]Grade) error {
		fresh, err := LoadQueue(client, mbid, s.Content)
		if err != nil {
			return err
		}
		if fresh.Date != s.Date {
			return fmt.Errorf("学习日期已变化，请重新启动学习")
		}
		learningTime := max(fresh.LearningTime, s.LearningTime)
		return client.SubmitItems(mbid, fresh.BuildSubmit(grades, learningTime))
	}}
}

// Enqueue snapshots the grades before the UI reuses its maps. The result channel
// is buffered so an interrupted UI never prevents the worker from draining.
func (s *Submitter) Enqueue(sess *Session, grades map[string]Grade) <-chan error {
	copied := make(map[string]Grade, len(grades))
	for id, g := range grades {
		copied[id] = g
	}
	snapshot := *sess
	result := make(chan error, 1)
	s.mu.Lock()
	previous := s.tail
	done := make(chan struct{})
	s.tail = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		s.mu.Lock()
		err := s.err
		s.mu.Unlock()
		if err == nil {
			err = s.submit(&snapshot, copied)
		}
		if err != nil {
			s.mu.Lock()
			if s.err == nil {
				s.err = fmt.Errorf("后台提交失败，后续提交已停止；本次未同步的学习进度尚未保存：%v", err)
			}
			err = s.err
			s.mu.Unlock()
		}
		result <- err
	}()
	return result
}

func (s *Submitter) Wait() error {
	s.mu.Lock()
	tail := s.tail
	s.mu.Unlock()
	if tail != nil {
		<-tail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Submitter) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

// Remaining is the optimistic queue used while writes catch up in the background.
// The submit worker always reads the authoritative COMPLETE queue before PUT.
func (s *Session) Remaining(grades map[string]Grade) *Session {
	next := *s
	filter := func(items []api.SyncItem) []api.SyncItem {
		out := make([]api.SyncItem, 0, len(items))
		for _, it := range items {
			if g := grades[it.ItemID]; g != Known && g != TooEasy {
				out = append(out, it)
			}
		}
		return out
	}
	next.AItems, next.CItems = filter(s.AItems), filter(s.CItems)
	return &next
}
