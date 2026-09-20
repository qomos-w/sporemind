package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"

	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig holds signing parameters.
type JWTConfig struct {
	Secret         []byte
	Issuer         string
	ExpiryTime     time.Duration
	RefreshExpiry  time.Duration
}

// DefaultJWTConfig returns a JWTConfig derived from, in priority order:
//  1. SPOREMIND_JWT_SECRET environment variable
//  2. A persisted secret file at <user-config>/sporemind/jwt-secret
//  3. A newly generated random secret (persisted to the file above)
//
// All callers within the same process receive the same secret.
func DefaultJWTConfig() JWTConfig {
	defaultSecretOnce.Do(func() {
		defaultSecret = loadOrGenerateSecret()
	})
	return JWTConfig{
		Secret:        defaultSecret,
		Issuer:        "sporemind",
		ExpiryTime:    2 * time.Hour,
		RefreshExpiry: 30 * 24 * time.Hour,
	}
}

func loadOrGenerateSecret() []byte {
	if s := os.Getenv("SPOREMIND_JWT_SECRET"); s != "" {
		return []byte(s)
	}
	if s := readPersistedSecret(); s != nil {
		return s
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("auth: failed to generate random JWT secret: %v", err))
	}
	secret := hex.EncodeToString(raw)
	if err := writePersistedSecret(secret); err != nil {
		log.Printf("WARNING: SPOREMIND_JWT_SECRET not set and could not persist generated secret: %v. Tokens will not survive restarts.", err)
	}
	return []byte(secret)
}

func secretFilePath() string {
	return filepath.Join(config.DataDir(), "jwt-secret")
}

func readPersistedSecret() []byte {
	data, err := os.ReadFile(secretFilePath())
	if err != nil {
		return nil
	}
	s := string(data)
	if len(s) >= 16 {
		return []byte(s)
	}
	return nil
}

func writePersistedSecret(secret string) error {
	dir := config.DataDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(secretFilePath(), []byte(secret), 0600)
}

// Claims carries the JWT custom claims for sporemind users.
type Claims struct {
	jwt.RegisteredClaims
	UserID   string   `json:"uid"`
	Username string   `json:"usr"`
	Roles    []string `json:"rol"`
}

// TokenPair holds the access token, refresh token, and their expiry.
type TokenPair struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// Manager handles JWT lifecycle.
type Manager struct {
	cfg JWTConfig
}

// NewManager creates a JWT manager with the given config.
func NewManager(cfg JWTConfig) *Manager {
	return &Manager{cfg: cfg}
}

// Issue generates a new JWT for the given user.
func (m *Manager) Issue(userID, username string, roles []string) (TokenPair, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.cfg.Issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.cfg.ExpiryTime)),
		},
		UserID:   userID,
		Username: username,
		Roles:    roles,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.cfg.Secret)
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: sign token: %w", err)
	}
	return TokenPair{AccessToken: signed, ExpiresAt: now.Add(m.cfg.ExpiryTime)}, nil
}

// Verify parses and validates a JWT, returning the embedded claims.
func (m *Manager) Verify(tokenString string) (Claims, error) {
	var claims Claims
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return m.cfg.Secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return Claims{}, ErrTokenExpired
		}
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}

// IssueRefreshToken generates a cryptographically random opaque refresh token.
// Unlike access tokens (JWT), refresh tokens carry no embedded claims —
// validation is lookup-only by the calling actor.
func (m *Manager) IssueRefreshToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("auth: failed to generate refresh token: %v", err))
	}
	return hex.EncodeToString(raw)
}

// RefreshExpiryTime returns the configured refresh token TTL.
func (m *Manager) RefreshExpiryTime() time.Duration {
	return m.cfg.RefreshExpiry
}
