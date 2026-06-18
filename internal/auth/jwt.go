// Package auth provides JWT issuing/verification, role-based access control
// middleware, local password login and Feishu OAuth login for the API plane.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims is the JWT payload carried by an authenticated session.
type Claims struct {
	Subject string `json:"sub"`
	Email   string `json:"email,omitempty"`
	Name    string `json:"name,omitempty"`
	Role    Role   `json:"role"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

// ErrInvalidToken is returned for any malformed, mis-signed or expired token.
var ErrInvalidToken = errors.New("auth: invalid token")

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Issue mints an HS256 JWT for the claims, valid for ttl from now.
func Issue(c Claims, secret string, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("auth: empty signing secret")
	}
	now := time.Now()
	c.Issued = now.Unix()
	c.Expires = now.Add(ttl).Unix()

	h, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signing := b64(h) + "." + b64(p)
	return signing + "." + b64(sign(signing, secret)), nil
}

// Parse verifies an HS256 JWT's signature and expiry and returns its claims.
func Parse(token, secret string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	expected := sign(parts[0]+"."+parts[1], secret)
	got, err := unb64(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}
	if subtle.ConstantTimeCompare(expected, got) != 1 {
		return nil, ErrInvalidToken
	}
	payload, err := unb64(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrInvalidToken
	}
	if c.Expires != 0 && time.Now().Unix() >= c.Expires {
		return nil, fmt.Errorf("%w: expired", ErrInvalidToken)
	}
	return &c, nil
}

func sign(msg, secret string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return m.Sum(nil)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
