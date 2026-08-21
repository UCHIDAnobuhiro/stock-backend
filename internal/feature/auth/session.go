package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

const (
	// DefaultRefreshTokenTTL はリフレッシュトークンの有効期限です。
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour

	// refreshTokenReuseGracePeriod は正規クライアントの並行更新を盗難と誤検知しないための猶予期間です。
	refreshTokenReuseGracePeriod = 30 * time.Second

	refreshTokenBytes = 32
)

// TokenPair はブラウザセッションへ発行するアクセストークンと
// リフレッシュトークンをまとめた値です。
type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// RefreshSession はサーバー管理のリフレッシュセッションです。
// TokenHash にはトークン本体ではなく SHA-256 ハッシュだけを保持します。
type RefreshSession struct {
	ID         string
	FamilyID   string
	UserID     int64
	TokenHash  []byte
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	RevokedAt  *time.Time
	ReplacedBy *string
	CreatedAt  time.Time
}

// JWTGenerator はJWTトークン生成のインターフェースを定義します。
// Goの慣例に従い、インターフェースはプロバイダー（transport/jwt）ではなくコンシューマー（usecase）が定義します。
type JWTGenerator interface {
	// GenerateToken は指定されたユーザーの署名済みJWTトークンを生成します。
	GenerateToken(userID int64, email string) (string, error)
}

// RefreshSessionRepository はリフレッシュセッションの永続化操作を定義します。
type RefreshSessionRepository interface {
	Create(ctx context.Context, session *RefreshSession) error
	Rotate(ctx context.Context, currentTokenHash []byte, now time.Time, nextFactory RefreshSessionFactory) error
	Revoke(ctx context.Context, tokenHash []byte, now time.Time) error
}

// RefreshSessionFactory はロック・検証済みセッションから次セッションを生成します。
type RefreshSessionFactory func(current RefreshSession, email string) (*RefreshSession, error)

// SessionIssuer はログイン成功時にトークンペアを発行します。
type SessionIssuer interface {
	Issue(ctx context.Context, userID int64, email string) (TokenPair, error)
}

// SessionManager はトークンペアの発行・更新・失効を扱います。
type SessionManager interface {
	SessionIssuer
	Refresh(ctx context.Context, refreshToken string) (TokenPair, error)
	Revoke(ctx context.Context, refreshToken string) error
}

// sessionService は短期JWTとサーバー管理リフレッシュセッションを統合します。
type sessionService struct {
	jwtGenerator JWTGenerator
	sessions     RefreshSessionRepository
	refreshTTL   time.Duration
	now          func() time.Time
}

var _ SessionManager = (*sessionService)(nil)

// NewSessionService は認証セッションサービスを生成します。
func NewSessionService(jwtGenerator JWTGenerator, sessions RefreshSessionRepository, refreshTTL time.Duration) *sessionService {
	return &sessionService{
		jwtGenerator: jwtGenerator,
		sessions:     sessions,
		refreshTTL:   refreshTTL,
		now:          time.Now,
	}
}

// Issue は新しいアクセストークンとリフレッシュセッションを発行します。
func (s *sessionService) Issue(ctx context.Context, userID int64, email string) (TokenPair, error) {
	now := s.now()
	rawToken, session, err := s.newRefreshSession(userID, "", now)
	if err != nil {
		return TokenPair{}, err
	}
	accessToken, err := s.jwtGenerator.GenerateToken(userID, email)
	if err != nil {
		return TokenPair{}, fmt.Errorf("failed to generate access token: %w", err)
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return TokenPair{}, fmt.Errorf("%w: failed to create refresh session: %v", ErrSessionUnavailable, err)
	}
	return TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     rawToken,
		RefreshExpiresAt: session.ExpiresAt,
	}, nil
}

// Refresh は有効なリフレッシュトークンを一度だけ消費し、新しいトークンペアへ交換します。
func (s *sessionService) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	if refreshToken == "" {
		return TokenPair{}, ErrRefreshTokenInvalid
	}
	currentHash := hashRefreshToken(refreshToken)
	now := s.now()
	var pair TokenPair
	var issueErr error
	err := s.sessions.Rotate(ctx, currentHash, now, func(current RefreshSession, email string) (*RefreshSession, error) {
		rawToken, next, err := s.newRefreshSession(current.UserID, current.FamilyID, now)
		if err != nil {
			issueErr = err
			return nil, err
		}
		accessToken, err := s.jwtGenerator.GenerateToken(current.UserID, email)
		if err != nil {
			issueErr = fmt.Errorf("failed to generate access token: %w", err)
			return nil, issueErr
		}
		pair = TokenPair{
			AccessToken:      accessToken,
			RefreshToken:     rawToken,
			RefreshExpiresAt: next.ExpiresAt,
		}
		return next, nil
	})
	if issueErr != nil {
		return TokenPair{}, issueErr
	}
	if err != nil {
		if errors.Is(err, ErrRefreshTokenInvalid) || errors.Is(err, ErrRefreshTokenExpired) ||
			errors.Is(err, ErrRefreshTokenReused) || errors.Is(err, ErrRefreshTokenConflict) {
			return TokenPair{}, err
		}
		return TokenPair{}, fmt.Errorf("%w: failed to rotate refresh session: %v", ErrSessionUnavailable, err)
	}
	return pair, nil
}

// Revoke は提示されたリフレッシュトークンの系列を失効させます。
// トークンがないログアウトも冪等に成功させます。
func (s *sessionService) Revoke(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	if err := s.sessions.Revoke(ctx, hashRefreshToken(refreshToken), s.now()); err != nil {
		return fmt.Errorf("%w: failed to revoke refresh session: %v", ErrSessionUnavailable, err)
	}
	return nil
}

func (s *sessionService) newRefreshSession(userID int64, familyID string, now time.Time) (string, *RefreshSession, error) {
	id, err := generateOpaqueToken()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate refresh session id: %w", err)
	}
	if familyID == "" {
		familyID, err = generateOpaqueToken()
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate refresh family id: %w", err)
		}
	}
	rawToken, err := generateOpaqueToken()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}
	return rawToken, &RefreshSession{
		ID:        id,
		FamilyID:  familyID,
		UserID:    userID,
		TokenHash: hashRefreshToken(rawToken),
		ExpiresAt: now.Add(s.refreshTTL),
	}, nil
}

func generateOpaqueToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
