package pcsserver

import (
	"fmt"
	"sync"
	"time"
)

type TaskStatus string

const (
	TaskPending TaskStatus = "pending"
	TaskRunning TaskStatus = "running"
	TaskDone    TaskStatus = "done"
	TaskFailed  TaskStatus = "failed"
)

type Task struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Status  TaskStatus `json:"status"`
	Message string     `json:"message"`
	StartAt time.Time  `json:"start_at"`
	EndAt   *time.Time `json:"end_at,omitempty"`
}

type taskManager struct {
	mu    sync.RWMutex
	tasks map[string]*Task
	seq   int
}

var mgr = &taskManager{tasks: make(map[string]*Task)}

func newTask(typ string) *Task {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.seq++
	t := &Task{
		ID:      fmt.Sprintf("%d-%d", time.Now().UnixNano()/1e6, mgr.seq),
		Type:    typ,
		Status:  TaskPending,
		StartAt: time.Now(),
	}
	mgr.tasks[t.ID] = t
	return t
}

func (t *Task) setRunning() {
	mgr.mu.Lock()
	t.Status = TaskRunning
	mgr.mu.Unlock()
}

func (t *Task) setDone(msg string) {
	mgr.mu.Lock()
	now := time.Now()
	t.Status = TaskDone
	t.Message = msg
	t.EndAt = &now
	mgr.mu.Unlock()
}

func (t *Task) setFailed(msg string) {
	mgr.mu.Lock()
	now := time.Now()
	t.Status = TaskFailed
	t.Message = msg
	t.EndAt = &now
	mgr.mu.Unlock()
}

func getTask(id string) (*Task, bool) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	t, ok := mgr.tasks[id]
	return t, ok
}

func listTasks() []*Task {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	list := make([]*Task, 0, len(mgr.tasks))
	for _, t := range mgr.tasks {
		list = append(list, t)
	}
	return list
}
