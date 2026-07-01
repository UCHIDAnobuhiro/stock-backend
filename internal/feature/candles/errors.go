package candles

import "fmt"

// ErrInvalidOutputSize は outputsize が許容範囲（1〜MaxOutputSize）外の場合に返されます。
// HTTP 経由では api/openapi.yaml の minimum/maximum 制約と openapivalidate
// ミドルウェアにより到達しないはずですが、リポジトリ層自身の不変条件として
// 防御的に検証します（Redis 障害時のフォールバックや将来の非HTTP呼び出し元を想定）。
var ErrInvalidOutputSize = fmt.Errorf("outputsize must be between 1 and %d", MaxOutputSize)
