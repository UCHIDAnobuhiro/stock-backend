package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/auth/sqlc"
)

// refreshSessionRepository はRefreshSessionRepositoryのPostgreSQL実装です。
type refreshSessionRepository struct {
	db *sql.DB
	q  *authsqlc.Queries
}

var (
	_ RefreshSessionRepository = (*refreshSessionRepository)(nil)
	_ ExpiredSessionRepository = (*refreshSessionRepository)(nil)
)

// NewRefreshSessionRepository はリフレッシュセッションリポジトリを生成します。
func NewRefreshSessionRepository(db *sql.DB) *refreshSessionRepository {
	return &refreshSessionRepository{db: db, q: authsqlc.New(db)}
}

// Create は新しいリフレッシュセッションを永続化します。
func (r *refreshSessionRepository) Create(ctx context.Context, session *RefreshSession) error {
	if session == nil {
		return errors.New("refresh session is nil")
	}
	row, err := r.q.CreateRefreshSession(ctx, authsqlc.CreateRefreshSessionParams{
		ID:        session.ID,
		FamilyID:  session.FamilyID,
		UserID:    session.UserID,
		TokenHash: session.TokenHash,
		ExpiresAt: session.ExpiresAt,
	})
	if err != nil {
		return err
	}
	*session = refreshSessionFromSQLC(row)
	return nil
}

// FindByTokenHash はハッシュに一致するリフレッシュセッションを返します。
func (r *refreshSessionRepository) FindByTokenHash(ctx context.Context, tokenHash []byte) (*RefreshSession, error) {
	row, err := r.q.FindRefreshSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRefreshTokenInvalid
		}
		return nil, err
	}
	session := refreshSessionFromSQLC(row)
	return &session, nil
}

// Rotate は現在のトークンを一度だけ消費し、同じ系列の次トークンを原子的に作成します。
// 消費直後の再提示は並行更新として扱い、猶予期間を超えた再利用では系列全体を失効させます。
func (r *refreshSessionRepository) Rotate(ctx context.Context, currentTokenHash []byte, now time.Time, nextFactory RefreshSessionFactory) error {
	if nextFactory == nil {
		return errors.New("refresh session factory is nil")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin refresh rotation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	qtx := r.q.WithTx(tx)
	row, err := qtx.LockRefreshSessionForRotation(ctx, currentTokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRefreshTokenInvalid
		}
		return err
	}
	current := refreshSessionFromRotationSQLC(row)
	if current.RevokedAt != nil {
		return ErrRefreshTokenInvalid
	}
	if current.ConsumedAt != nil {
		if !now.After(current.ConsumedAt.Add(refreshTokenReuseGracePeriod)) {
			return ErrRefreshTokenConflict
		}
		if err := qtx.RevokeRefreshSessionFamily(ctx, authsqlc.RevokeRefreshSessionFamilyParams{
			FamilyID:  current.FamilyID,
			RevokedAt: sql.NullTime{Time: now, Valid: true},
		}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit refresh reuse revocation: %w", err)
		}
		committed = true
		return ErrRefreshTokenReused
	}
	if !now.Before(current.ExpiresAt) {
		return ErrRefreshTokenExpired
	}

	next, err := nextFactory(current, row.Email)
	if err != nil {
		return err
	}
	if next == nil {
		return errors.New("next refresh session is nil")
	}
	next.UserID = current.UserID
	next.FamilyID = current.FamilyID

	if _, err := qtx.CreateRefreshSession(ctx, authsqlc.CreateRefreshSessionParams{
		ID:        next.ID,
		FamilyID:  next.FamilyID,
		UserID:    next.UserID,
		TokenHash: next.TokenHash,
		ExpiresAt: next.ExpiresAt,
	}); err != nil {
		return err
	}
	if err := qtx.ConsumeRefreshSession(ctx, authsqlc.ConsumeRefreshSessionParams{
		ID:         current.ID,
		ConsumedAt: sql.NullTime{Time: now, Valid: true},
		ReplacedBy: sql.NullString{String: next.ID, Valid: true},
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit refresh rotation: %w", err)
	}
	committed = true
	return nil
}

func refreshSessionFromRotationSQLC(row authsqlc.LockRefreshSessionForRotationRow) RefreshSession {
	return RefreshSession{
		ID:         row.ID,
		FamilyID:   row.FamilyID,
		UserID:     row.UserID,
		TokenHash:  row.TokenHash,
		ExpiresAt:  row.ExpiresAt,
		ConsumedAt: nullTimePointer(row.ConsumedAt),
		RevokedAt:  nullTimePointer(row.RevokedAt),
		ReplacedBy: nullStringPointer(row.ReplacedBy),
		CreatedAt:  row.CreatedAt,
	}
}

// Revoke はトークンが属する系列を失効させます。未知のトークンは冪等に成功します。
func (r *refreshSessionRepository) Revoke(ctx context.Context, tokenHash []byte, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin refresh revocation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	qtx := r.q.WithTx(tx)
	current, err := qtx.LockRefreshSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit unknown refresh revocation: %w", err)
			}
			committed = true
			return nil
		}
		return err
	}
	if err := qtx.RevokeRefreshSessionFamily(ctx, authsqlc.RevokeRefreshSessionFamilyParams{
		FamilyID:  current.FamilyID,
		RevokedAt: sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit refresh revocation: %w", err)
	}
	committed = true
	return nil
}

// DeleteExpired は指定時刻より前に期限切れとなったセッションを削除します。
func (r *refreshSessionRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	return r.q.DeleteExpiredRefreshSessions(ctx, before)
}

func refreshSessionFromSQLC(row authsqlc.RefreshSession) RefreshSession {
	return RefreshSession{
		ID:         row.ID,
		FamilyID:   row.FamilyID,
		UserID:     row.UserID,
		TokenHash:  row.TokenHash,
		ExpiresAt:  row.ExpiresAt,
		ConsumedAt: nullTimePointer(row.ConsumedAt),
		RevokedAt:  nullTimePointer(row.RevokedAt),
		ReplacedBy: nullStringPointer(row.ReplacedBy),
		CreatedAt:  row.CreatedAt,
	}
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
