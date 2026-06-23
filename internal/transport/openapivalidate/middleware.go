// Package openapivalidate は OpenAPI スペックを単一ソースとして、受信リクエストを
// 実行時に検証する net/http ミドルウェアを提供します。
// バリデーション制約（required / format / minLength 等）は api/openapi.yaml に集約し、
// Go 側に検証タグを持たせません。
package openapivalidate

import (
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	apispec "github.com/UCHIDAnobuhiro/stock-backend/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// skipPaths は OpenAPI バリデーションを適用しないパスです。
// /v1/logo/detect は multipart/form-data で、ハンドラ側が独自に 10MB の
// ストリーム制御（http.MaxBytesReader）を行うため、kin-openapi による
// multipart パースとの二重読みを避けて検証をスキップします。
var skipPaths = map[string]bool{
	"/v1/logo/detect": true,
}

// New は埋め込み OpenAPI スペックに基づくリクエストバリデーションミドルウェアを生成します。
// スペックのロード・検証に失敗した場合はエラーを返します（起動時に検出）。
func New() (func(http.Handler) http.Handler, error) {
	spec, err := apispec.Load()
	if err != nil {
		return nil, err
	}

	opts := &nethttpmiddleware.Options{
		Options: openapi3filter.Options{
			// 認証は jwt / csrf ミドルウェアが担うため、検証層では認証を no-op にする。
			// spec の protected route は security: [cookieAuth] を宣言しており、
			// これを設定しないと openapi3filter が認証器不在でエラーになる。
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
		// バリデーション失敗時は詳細をログに残しつつ、クライアントへは汎用文言を返す
		// （スキーマ内部情報を外部に漏らさない）。
		ErrorHandler: func(w http.ResponseWriter, message string, statusCode int) {
			slog.Warn("OpenAPI リクエストバリデーション失敗", "message", message, "status", statusCode)
			httpx.WriteJSON(w, statusCode, api.ErrorResponse{Error: "invalid request"})
		},
		// servers の Host 検証を無効化する。実行環境（localhost / Cloud Run のホスト名）で
		// Host が変わると "no matching operation" の 400 になるため、パスベース検証のみ行う。
		DoNotValidateServers: true,
	}

	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, opts)

	// multipart エンドポイントや CORS preflight は検証対象外にするためのラッパー。
	// （nethttp-middleware v1.1.2 には Skipper オプションが無いため自前で分岐する）
	return func(next http.Handler) http.Handler {
		validated := validator(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || skipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			validated.ServeHTTP(w, r)
		})
	}, nil
}
