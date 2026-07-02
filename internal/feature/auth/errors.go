package auth

import "errors"

var (
	// ErrUserNotFound はメールアドレスまたはIDでユーザーが見つからない場合に返されます。
	ErrUserNotFound = errors.New("user not found")

	// ErrEmailAlreadyExists は既に存在するメールアドレスでユーザーを作成しようとした場合に返されます。
	ErrEmailAlreadyExists = errors.New("email already exists")

	// ErrInvalidCredentials はメールアドレスまたはパスワードが正しくない場合に返されます。
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrStateNotFound はOAuthのstateが存在しない・期限切れの場合に返されます。
	ErrStateNotFound = errors.New("oauth state not found or expired")

	// ErrOAuthEmailUnavailable はOAuthプロバイダーから検証済みメールアドレスが取得できない場合に返されます。
	ErrOAuthEmailUnavailable = errors.New("verified email not available from oauth provider")

	// ErrOAuthEmailConflict はOAuthログインのメールアドレスが既存アカウントに登録済みで、
	// 本人確認なしの自動リンクを拒否した場合に返されます。
	ErrOAuthEmailConflict = errors.New("email already registered to an existing account")

	// ErrUnknownProvider は未対応のOAuthプロバイダーが指定された場合に返されます。
	ErrUnknownProvider = errors.New("unknown oauth provider")
)
