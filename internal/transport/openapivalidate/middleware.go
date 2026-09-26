// Package openapivalidate は OpenAPI スペックを単一ソースとして、受信リクエストを
// 実行時に検証する net/http ミドルウェアを提供します。
// バリデーション制約（required / format / minLength 等）は api/openapi.yaml に集約し、
// Go 側に検証タグを持たせません。
package openapivalidate

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	apispec "github.com/UCHIDAnobuhiro/stock-backend/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/api"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/transport/httpx"
)

// maxRequestBodyBytes はJSON等の検証対象本文の上限（1 MiB）。
// スキーマ検証は本文を全て読むため、フィールドの長さ検証より前に制限する。
// multipart画像はこの検証をスキップし、画像ハンドラー側の上限を使用する。
const maxRequestBodyBytes int64 = 1 << 20

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
			// 認証とCSRFの実検証は前段の jwt / csrf ミドルウェアが担う。
			// OpenAPI の Security Requirement は契約記述に使用し、この検証層では no-op にする。
			// これを設定しないと openapi3filter が各 Security Scheme の認証器不在でエラーになる。
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
		// 入力値を含み得るエラー文字列は記録せず、スキーマ由来の項目名と違反コードだけを残す。
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			var sizeErr *http.MaxBytesError
			if errors.As(err, &sizeErr) {
				httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, api.ErrorResponse{Error: "request body too large"})
				return
			}
			field, reason := validationLogFields(err)
			slog.Warn("OpenAPI リクエストバリデーション失敗", "field", field, "reason", reason, "status", opts.StatusCode)
			httpx.WriteJSON(w, opts.StatusCode, api.ErrorResponse{Error: "invalid request"})
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
			if r.ContentLength > maxRequestBodyBytes {
				httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, api.ErrorResponse{Error: "request body too large"})
				return
			}
			// Content-Lengthなし（chunked等）でも実際の読み込み量を制限する。
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
			}
			validated.ServeHTTP(w, r)
		})
	}, nil
}

// validationLogFields は検証器の自由文・入力値をログに渡さない。
func validationLogFields(err error) (field, reason string) {
	field, reason = "request", "invalid_request"
	var requestErr *openapi3filter.RequestError
	if !errors.As(err, &requestErr) {
		return field, reason
	}
	if requestErr.Parameter != nil {
		field = requestErr.Parameter.Name // OpenAPI 定義から渡された Parameter の名前
		if errors.Is(err, openapi3filter.ErrInvalidRequired) {
			reason = "required"
		}
	} else if requestErr.RequestBody != nil {
		field = "body"
	}

	var schemaErr *openapi3.SchemaError
	if !errors.As(err, &schemaErr) {
		return field, reason
	}
	if code := schemaViolationCode(schemaErr.SchemaField); code != "" {
		reason = code
	}
	if requestErr.RequestBody != nil {
		field = bodySchemaField(requestErr.RequestBody, schemaErr.JSONPointer())
	}
	return field, reason
}

func schemaViolationCode(schemaField string) string {
	switch schemaField {
	case "required", "type", "format", "minLength", "maxLength", "pattern", "enum", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties", "nullable", "oneOf", "anyOf", "allOf", "const", "not":
		return schemaField
	default:
		return ""
	}
}

// JSONPointer の各部分は入力由来のため、OpenAPI に存在する Properties/Items だけを辿る。
func bodySchemaField(body *openapi3.RequestBody, path []string) string {
	media := body.Content["application/json"]
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		return "body"
	}
	schema := media.Schema
	field := "body"
	for _, part := range path {
		if schema.Value == nil {
			return "body"
		}
		if property := schema.Value.Properties[part]; property != nil {
			field, schema = part, property
			continue
		}
		if schema.Value.Items != nil {
			if _, err := strconv.ParseUint(part, 10, 64); err == nil {
				schema = schema.Value.Items
				continue
			}
		}
		return "body"
	}
	return field
}
