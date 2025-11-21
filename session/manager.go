package session

import (
	"sync"
	"time"
)

type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

func (m *Manager) CreateSession(duration time.Duration) (*Session, error) {
	s, err := NewSession(duration)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()

	// Remove from map when stopped (this is a bit tricky with the current Stop implementation, 
    // ideally Stop should callback to manager, but for now we can just let it be or add a callback)
    // Let's add a cleanup routine or just check existence. 
    // Better: wrap Stop to remove from manager.
    
    // For simplicity in this MVP, we'll just leave it in the map until we explicitly list/clean or 
    // maybe we can have a background cleaner.
    // Actually, let's just add a Remove method and call it from the HTTP handler or auto-cleanup.
    
    // Refined approach: The session handles its own process death, but the manager needs to know to remove it from the map.
    // We can pass a callback to NewSession? Or just have a ticker in Manager that cleans up expired sessions.
    
	return s, nil
}

func (m *Manager) GetSession(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) DeleteSession(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		s.Stop()
		delete(m.sessions, id)
	}
}

func (m *Manager) ListSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	return list
}
