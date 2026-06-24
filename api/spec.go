// Package apispec は OpenAPI スペック（openapi.yaml）をバイナリに埋め込み、
// パース済みの *openapi3.T として提供します。
// リクエストバリデーションミドルウェア（internal/transport/openapivalidate）が
// この契約を単一ソースとして実行時検証に利用します。
package apispec

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
)

func init() {
	// kin-openapi はデフォルトで email など一部の string format を検証しない
	// （byte / date / date-time / int32 / int64 のみ登録済み）。
	// spec の `format: email` を実行時に強制するため、email バリデータを登録する。
	openapi3.DefineStringFormatValidator("email", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForEmail))
}

// specYAML は OpenAPI 3.0 スペックの生データです。
// 埋め込みディレクティブは同一ディレクトリ以下しか参照できないため、
// このファイルは openapi.yaml と同じ api/ に配置しています。
//
//go:embed openapi.yaml
var specYAML []byte

// Load は埋め込まれた OpenAPI スペックをパース・検証して返します。
// スペック自体の妥当性検証（doc.Validate）まで行うため、契約の不整合は
// アプリ起動時に検出できます。
func Load() (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(specYAML)
	if err != nil {
		return nil, fmt.Errorf("OpenAPI スペックのパースに失敗: %w", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("OpenAPI スペックの検証に失敗: %w", err)
	}
	return doc, nil
}
