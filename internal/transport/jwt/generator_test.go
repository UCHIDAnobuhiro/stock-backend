package jwt

import (
	"strconv"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// TestNewGenerator は各種設定でGeneratorが正しく生成されることを検証します。
func TestNewGenerator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		secret     string
		expiration time.Duration
	}{
		{"standard config", "my-secret-key", time.Hour},
		{"long expiration", "secret", 24 * time.Hour * 30},
		{"short expiration", "s", time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gen := NewGenerator(tt.secret, tt.expiration)

			if gen == nil {
				t.Fatal("expected generator to be non-nil")
			}
			if string(gen.secret) != tt.secret {
				t.Errorf("expected secret %q, got %q", tt.secret, string(gen.secret))
			}
			if gen.expiration != tt.expiration {
				t.Errorf("expected expiration %v, got %v", tt.expiration, gen.expiration)
			}
		})
	}
}

// TestGenerator_GenerateToken は生成されたJWTトークンが有効で正しいクレームを含むことを検証します。
func TestGenerator_GenerateToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		userID     int64
		email      string
		expiration time.Duration
	}{
		{"basic user", 1, "user@example.com", time.Hour},
		{"user with special email", 42, "user+tag@example.com", time.Hour},
		{"large user id", 9223372036854775807, "test@test.com", 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gen := NewGenerator("test-secret", tt.expiration)
			tokenStr, err := gen.GenerateToken(tt.userID, tt.email)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tokenStr == "" {
				t.Fatal("expected non-empty token")
			}

			// Verify the token can be parsed
			token, err := gojwt.Parse(tokenStr, func(t *gojwt.Token) (any, error) {
				return []byte("test-secret"), nil
			})
			if err != nil {
				t.Fatalf("failed to parse token: %v", err)
			}
			if !token.Valid {
				t.Error("expected token to be valid")
			}

			// Verify claims
			claims, ok := token.Claims.(gojwt.MapClaims)
			if !ok {
				t.Fatal("expected MapClaims")
			}

			if sub, ok := claims["sub"].(string); !ok || sub != strconv.FormatInt(tt.userID, 10) {
				t.Errorf("expected sub %d, got %v", tt.userID, claims["sub"])
			}
			if email, ok := claims["email"].(string); !ok || email != tt.email {
				t.Errorf("expected email %q, got %v", tt.email, claims["email"])
			}
			if _, ok := claims["exp"]; !ok {
				t.Error("expected exp claim to be set")
			}
			if _, ok := claims["iat"]; !ok {
				t.Error("expected iat claim to be set")
			}
		})
	}
}

// TestGenerator_GenerateToken_SigningMethod はトークンがHS256署名アルゴリズムで署名されていることを検証します。
func TestGenerator_GenerateToken_SigningMethod(t *testing.T) {
	t.Parallel()

	gen := NewGenerator("test-secret", time.Hour)
	tokenStr, err := gen.GenerateToken(1, "test@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	token, err := gojwt.Parse(tokenStr, func(tok *gojwt.Token) (any, error) {
		// Verify signing method is HMAC
		if _, ok := tok.Method.(*gojwt.SigningMethodHMAC); !ok {
			t.Errorf("unexpected signing method: %v", tok.Header["alg"])
		}
		return []byte("test-secret"), nil
	})
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}
	if !token.Valid {
		t.Error("expected token to be valid")
	}
}

// TestGenerator_GenerateToken_Expiration はトークンのexp・iatクレームが正しい時刻範囲内であることを検証します。
func TestGenerator_GenerateToken_Expiration(t *testing.T) {
	t.Parallel()

	expiration := 2 * time.Hour
	gen := NewGenerator("test-secret", expiration)

	before := time.Now().Truncate(time.Second)
	tokenStr, err := gen.GenerateToken(1, "test@example.com")
	after := time.Now().Truncate(time.Second).Add(time.Second) // Add 1 second buffer

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	token, _ := gojwt.Parse(tokenStr, func(t *gojwt.Token) (any, error) {
		return []byte("test-secret"), nil
	})

	claims := token.Claims.(gojwt.MapClaims)

	// Check exp is within expected range (using Unix timestamps for comparison)
	expUnix := int64(claims["exp"].(float64))
	expectedMinUnix := before.Add(expiration).Unix()
	expectedMaxUnix := after.Add(expiration).Unix()

	if expUnix < expectedMinUnix || expUnix > expectedMaxUnix {
		t.Errorf("exp %d not in expected range [%d, %d]", expUnix, expectedMinUnix, expectedMaxUnix)
	}

	// Check iat is within expected range
	iatUnix := int64(claims["iat"].(float64))
	if iatUnix < before.Unix() || iatUnix > after.Unix() {
		t.Errorf("iat %d not in expected range [%d, %d]", iatUnix, before.Unix(), after.Unix())
	}
}

// TestGenerator_GenerateToken_DifferentUsersProduceDifferentTokens は異なるユーザーに対して異なるトークンが生成されることを検証します。
func TestGenerator_GenerateToken_DifferentUsersProduceDifferentTokens(t *testing.T) {
	t.Parallel()

	gen := NewGenerator("test-secret", time.Hour)

	token1, _ := gen.GenerateToken(1, "user1@example.com")
	token2, _ := gen.GenerateToken(2, "user2@example.com")

	if token1 == token2 {
		t.Error("expected different tokens for different users")
	}
}
