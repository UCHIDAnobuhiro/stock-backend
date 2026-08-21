package httpx

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	t.Run("JSONを書き込み従来のエスケープと末尾改行を維持する", func(t *testing.T) {
		t.Parallel()

		w := httptest.NewRecorder()
		WriteJSON(w, http.StatusCreated, struct {
			Text string `json:"text"`
		}{Text: "<script>&\u2028"})

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Equal(t, "{\"text\":\"\\u003cscript\\u003e\\u0026\\u2028\"}\n", w.Body.String())
	})

	t.Run("値がnilならステータスだけを書き込む", func(t *testing.T) {
		t.Parallel()

		w := httptest.NewRecorder()
		WriteJSON(w, http.StatusNoContent, nil)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Empty(t, w.Body.String())
	})
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()

	type requestBody struct {
		Name string `json:"name"`
	}

	tests := []struct {
		name    string
		body    []byte
		want    requestBody
		wantErr bool
	}{
		{
			name: "正常なJSON",
			body: []byte(`{"name":"gopher"}`),
			want: requestBody{Name: "gopher"},
		},
		{
			name: "末尾の空白は許可する",
			body: []byte("{\"name\":\"gopher\"}\n\t"),
			want: requestBody{Name: "gopher"},
		},
		{
			name: "未知フィールドは従来どおり無視する",
			body: []byte(`{"name":"gopher","unknown":true}`),
			want: requestBody{Name: "gopher"},
		},
		{
			name: "フィールド名は大文字小文字を区別する",
			body: []byte(`{"Name":"ignored"}`),
			want: requestBody{},
		},
		{
			name:    "重複フィールドを拒否する",
			body:    []byte(`{"name":"first","name":"second"}`),
			wantErr: true,
		},
		{
			name:    "トップレベルJSONの後続データを拒否する",
			body:    []byte(`{"name":"first"} {"name":"second"}`),
			wantErr: true,
		},
		{
			name:    "不正UTF-8を拒否する",
			body:    []byte("{\"name\":\"\xff\"}"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			var got requestBody
			err := DecodeJSON(r, &got)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestClientIP は ClientIP が context 格納済みIPを優先し、
// 未格納の場合は RemoteAddr にフォールバックすることを検証します。
func TestClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		remoteAddr   string
		contextIP    string // 空文字なら context に格納しない
		wantClientIP string
	}{
		{
			name:         "context にIPがあればそれを返す",
			remoteAddr:   "203.0.113.9:12345",
			contextIP:    "198.51.100.7",
			wantClientIP: "198.51.100.7",
		},
		{
			name:         "context が空なら RemoteAddr のホスト部を返す",
			remoteAddr:   "203.0.113.9:12345",
			contextIP:    "",
			wantClientIP: "203.0.113.9",
		},
		{
			name:         "RemoteAddr にポートがなければそのまま返す",
			remoteAddr:   "203.0.113.9",
			contextIP:    "",
			wantClientIP: "203.0.113.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.contextIP != "" {
				req = req.WithContext(WithClientIP(req.Context(), tt.contextIP))
			}

			assert.Equal(t, tt.wantClientIP, ClientIP(req))
		})
	}
}
