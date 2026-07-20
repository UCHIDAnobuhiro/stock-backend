package httpratelimit

// このファイルは外部テストパッケージ向けのモック設定ヘルパーを提供します。
// Lua スクリプトの SHA1（scriptHash）を非公開に保ったままモック設定を可能にするため、
// サブパッケージ（httpratelimittest など）ではなく本体パッケージに置いています。
// 本体に redismock の import が入る代償はあるものの、redismock は pure Go かつ軽量で、
// リンカーのデッドコード除去により本番バイナリへの影響は限定的です。

import "github.com/go-redis/redismock/v9"

// ExpectAllow は redismock に対して Allow() 呼び出し時の Lua スクリプト返り値の期待値を設定します。
// 外部テストパッケージが Redis を立てずに rate limiter の挙動を検証する用途に使います。
// allowed=false を指定すると Allow() が Result{Allowed: false} を返す状態を再現します。
// count は当該ウィンドウ内の現在件数（Lua スクリプトの 2 番目の返り値）です。
func ExpectAllow(mock redismock.ClientMock, key string, allowed bool, count int64) {
	allowedInt := int64(0)
	if allowed {
		allowedInt = 1
	}
	mock.ExpectEvalSha(scriptHash(), []string{key}, "_", "_", "_", "_", "_").
		SetVal([]interface{}{allowedInt, count})
}

// ExpectAllowError は redismock に対して Allow() 呼び出し時の Lua スクリプト実行がエラーになる
// 状態を設定します。外部テストパッケージが Redis 障害時（FailClosedによる503等）の挙動を
// 検証する用途に使います。
func ExpectAllowError(mock redismock.ClientMock, key string, err error) {
	mock.ExpectEvalSha(scriptHash(), []string{key}, "_", "_", "_", "_", "_").SetErr(err)
}
