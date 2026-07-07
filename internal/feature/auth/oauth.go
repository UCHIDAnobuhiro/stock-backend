package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const oauthStateTTL = 10 * time.Minute

// oauthUsecase はOAuth2認証フローのビジネスロジックを実装します。
type oauthUsecase struct {
	users      UserRepository
	oauthAccts OAuthAccountRepository
	creator    OAuthUserCreator
	stateStore OAuthStateStore
	jwtGen     JWTGenerator
	providers  map[string]OAuthProvider
	hooks      []UserCreatedHook
}

// NewOAuthUsecase はoauthUsecaseの新しいインスタンスを生成します。
// providers は map[providerName]OAuthProvider として渡します。
// creator は新規ユーザーとOAuthAccountをトランザクション内で作成します。
// hooks にはユーザー新規作成後に呼び出すフックを渡します（例: watchlistUC）。
func NewOAuthUsecase(
	users UserRepository,
	oauthAccts OAuthAccountRepository,
	creator OAuthUserCreator,
	stateStore OAuthStateStore,
	jwtGen JWTGenerator,
	providers map[string]OAuthProvider,
	hooks ...UserCreatedHook,
) *oauthUsecase {
	return &oauthUsecase{
		users:      users,
		oauthAccts: oauthAccts,
		creator:    creator,
		stateStore: stateStore,
		jwtGen:     jwtGen,
		providers:  providers,
		hooks:      hooks,
	}
}

// BeginAuth は指定プロバイダーのOAuth2認可URLと、生成した state を返します。
// PKCE（S256）のcodeVerifierとstateを生成しRedisに保存します。
// 返却した state は呼び出し側（HTTPハンドラー）でブラウザ側 Cookie にも保存し、
// コールバック時にクエリの state と照合することでログイン CSRF を防ぎます。
func (uc *oauthUsecase) BeginAuth(ctx context.Context, providerName string) (authURL, state string, err error) {
	provider, ok := uc.providers[providerName]
	if !ok {
		return "", "", ErrUnknownProvider
	}

	state, err = generateRandomBase64(32)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate oauth state: %w", err)
	}

	codeVerifier, err := generateRandomBase64(32)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate code verifier: %w", err)
	}

	// S256: codeChallenge = BASE64URL(SHA256(codeVerifier))
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if err := uc.stateStore.SaveState(ctx, state, codeVerifier, oauthStateTTL); err != nil {
		return "", "", fmt.Errorf("failed to save oauth state: %w", err)
	}

	return provider.AuthorizationURL(state, codeChallenge), state, nil
}

// HandleCallback はプロバイダーから返却されたcodeとstateを検証し、
// JWTトークンを返します。同メールの既存ユーザーが存在する場合は自動リンクせず
// ErrOAuthEmailConflict を返します（アカウント乗っ取り防止）。
func (uc *oauthUsecase) HandleCallback(ctx context.Context, providerName, code, state string) (string, error) {
	provider, ok := uc.providers[providerName]
	if !ok {
		return "", ErrUnknownProvider
	}

	// stateの検証と消費（リプレイ攻撃防止のため atomic に削除）
	codeVerifier, err := uc.stateStore.ConsumeState(ctx, state)
	if err != nil {
		return "", err
	}

	// authorization code を ユーザー情報に交換
	info, err := provider.ExchangeCode(ctx, code, codeVerifier)
	if err != nil {
		return "", fmt.Errorf("oauth code exchange failed: %w", err)
	}
	// プロバイダー返却メールを正規化し、既存ユーザー検索・新規作成・JWT 生成を
	// すべて正規化済みメールで行う（大小文字違いによる重複アカウントを防ぐ）。
	info.Email = NormalizeEmail(info.Email)
	if info.Email == "" {
		return "", ErrOAuthEmailUnavailable
	}

	userID, err := uc.findOrCreateUser(ctx, providerName, info)
	if err != nil {
		return "", err
	}

	tok, err := uc.jwtGen.GenerateToken(userID, info.Email)
	if err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	return tok, nil
}

// findOrCreateUser は既存OAuthAccountを探し、なければユーザーを新規作成します。
// 同メールの既存ユーザーが存在する場合は ErrOAuthEmailConflict を返します
// （本人確認なしの自動リンクはアカウント乗っ取りリスクがあるため行わない）。
func (uc *oauthUsecase) findOrCreateUser(ctx context.Context, providerName string, info *OAuthUserInfo) (int64, error) {
	// 既存OAuthAccountで検索
	acct, err := uc.oauthAccts.FindByProvider(ctx, providerName, info.ProviderUID)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return 0, fmt.Errorf("oauth account lookup failed: %w", err)
	}
	if acct != nil {
		return acct.UserID, nil
	}

	// OAuthAccountなし → メールで既存ユーザーを検索
	user, err := uc.users.FindByEmail(ctx, info.Email)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return 0, fmt.Errorf("user lookup by email failed: %w", err)
	}

	if user != nil {
		// 同メールの既存アカウントへの自動リンクは行わない。
		// プロバイダーの検証済みメールであっても、メールアドレスの再割当て
		// （例: 退職者メールの別人への再利用）が起こり得るため、メール一致だけを
		// 根拠にリンクするとアカウント乗っ取りが成立してしまう。
		// 明示的な本人確認を伴うリンク承認フローが実装されるまでは一律拒否する。
		return 0, ErrOAuthEmailConflict
	}

	// 新規ユーザー作成（OAuth専用: Password = nil）
	// UserとOAuthAccountをトランザクション内で原子的に作成し、
	// 片方だけ残る不整合を防ぐ。
	newUser := &User{Email: info.Email}
	if err := uc.creator.CreateUserWithOAuthAccount(ctx, newUser, &OAuthAccount{
		Provider:    providerName,
		ProviderUID: info.ProviderUID,
	}); err != nil {
		// FindByEmail の後に同メールのユーザーが作成された並行レースでは
		// email のユニーク制約違反となるため、上と同じ理由で拒否として扱う。
		if errors.Is(err, ErrEmailAlreadyExists) {
			return 0, ErrOAuthEmailConflict
		}
		return 0, fmt.Errorf("failed to create user with oauth account: %w", err)
	}

	// 新規作成後フック呼び出し（例: ウォッチリスト初期化）
	// フック失敗はユーザー作成自体には影響しないため非致命的とし、ログのみ記録する。
	for _, hook := range uc.hooks {
		if err := hook.OnUserCreated(ctx, newUser.ID); err != nil {
			slog.Error("post-create hook failed", "user_id", newUser.ID, "error", err)
		}
	}

	return newUser.ID, nil
}

// generateRandomBase64 は n バイトのランダム値をURLセーフなBase64文字列で返します。
func generateRandomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
