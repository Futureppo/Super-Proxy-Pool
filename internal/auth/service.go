package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"super-proxy-pool/internal/settings"
)

const SessionCookieName = "spp_session"

type session struct {
	ExpiresAt time.Time
}

type Service struct {
	settings      *settings.Service
	sessionMaxAge time.Duration
	mu            sync.RWMutex
	sessions      map[string]session
}

func NewService(settingsSvc *settings.Service, sessionMaxAgeSec int) *Service {
	return &Service{
		settings:      settingsSvc,
		sessionMaxAge: time.Duration(sessionMaxAgeSec) * time.Second,
		sessions:      make(map[string]session),
	}
}

func HashPassword(password string) (string, error) {
	data, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(data), err
}

func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Service) Login(ctx context.Context, password string) (string, error) {
	current, err := s.settings.Get(ctx)
	if err != nil {
		return "", err
	}
	if !VerifyPassword(current.PasswordHash, password) {
		return "", errors.New("password incorrect")
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessions[token] = session{ExpiresAt: time.Now().Add(s.sessionMaxAge)}
	s.mu.Unlock()
	return token, nil
}

func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *Service) ChangePassword(ctx context.Context, oldPassword, newPassword string) error {
	current, err := s.settings.Get(ctx)
	if err != nil {
		return err
	}
	if !VerifyPassword(current.PasswordHash, oldPassword) {
		return errors.New("old password incorrect")
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.settings.UpdatePasswordHash(ctx, hash); err != nil {
		return err
	}
	s.InvalidateAllSessions()
	return nil
}

func (s *Service) InvalidateAllSessions() {
	s.mu.Lock()
	s.sessions = make(map[string]session)
	s.mu.Unlock()
}

func (s *Service) IsAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return false
	}
	now := time.Now()
	s.mu.RLock()
	current, ok := s.sessions[cookie.Value]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if current.ExpiresAt.After(now) {
		return true
	}
	s.mu.Lock()
	if current, ok := s.sessions[cookie.Value]; ok && !current.ExpiresAt.After(now) {
		delete(s.sessions, cookie.Value)
	}
	s.mu.Unlock()
	return false
}

func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.IsAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			writeUnauthorizedJSON(w)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func (s *Service) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.sessionMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func writeUnauthorizedJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
}
