package myjwt

import (
	"GopherAI/config"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type Claims struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

func GenerateToken(id int64, username string) (string, error) {
	return generateToken(id, username, time.Duration(config.GetConfig().ExpireDuration)*time.Hour)
}

// GenerateEphemeralToken creates a 15-minute token for a bounded local runtime probe.
// Callers must never persist or return this token in reports.
func GenerateEphemeralToken(id int64, username string) (string, error) {
	return generateToken(id, username, 15*time.Minute)
}

func generateToken(id int64, username string, ttl time.Duration) (string, error) {
	claims := Claims{
		ID:       id,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			Issuer:    config.GetConfig().Issuer,
			Subject:   config.GetConfig().Subject,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	// 生成 token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GetConfig().Key))
}

// ParseToken 解析Token
func ParseToken(token string) (string, bool) {
	claims := new(Claims)
	t, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.GetConfig().Key), nil
	})
	if !t.Valid || err != nil || claims == nil {
		return "", false
	}
	return claims.Username, true
}
