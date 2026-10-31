package gateway

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"
)

// Admin sessions are intentionally process-local. A restart invalidates every
// browser session; API automation should keep using the long-lived admin token.
const adminSessionTTL = 12 * time.Hour

type adminSession struct {
	Username string
	Expires  time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[[32]byte]adminSession
}

func (s *sessionStore) create(username string) (string, adminSession) {
	token := ID("sess_") + ID("")
	session := adminSession{Username: username, Expires: time.Now().Add(adminSessionTTL)}
	hash := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[[32]byte]adminSession{}
	}
	s.pruneLocked(time.Now())
	s.sessions[hash] = session
	return token, session
}

func (s *sessionStore) get(token string) (adminSession, bool) {
	hash := sha256.Sum256([]byte(token))
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[hash]
	if !ok {
		return adminSession{}, false
	}
	if !now.Before(session.Expires) {
		delete(s.sessions, hash)
		return adminSession{}, false
	}
	return session, true
}

func (s *sessionStore) delete(token string) {
	hash := sha256.Sum256([]byte(token))
	s.mu.Lock()
	delete(s.sessions, hash)
	s.mu.Unlock()
}

func (s *sessionStore) pruneLocked(now time.Time) {
	for hash, session := range s.sessions {
		if !now.Before(session.Expires) {
			delete(s.sessions, hash)
		}
	}
}

type adminAuth struct {
	Kind     string
	Username string
	Expires  time.Time
	Token    string
}

type adminAuthContextKey struct{}

func withAdminAuth(ctx context.Context, auth adminAuth) context.Context {
	return context.WithValue(ctx, adminAuthContextKey{}, auth)
}

func adminAuthFrom(ctx context.Context) (adminAuth, bool) {
	auth, ok := ctx.Value(adminAuthContextKey{}).(adminAuth)
	return auth, ok
}
