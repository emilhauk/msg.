package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const cookieName = "session"

var ErrInvalidToken = errors.New("auth: invalid session token")

// SignToken creates a signed token: <random>.<hmac>.
func SignToken(secret []byte) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	sig := sign(secret, token)
	return token + "." + sig, nil
}

// VerifyToken verifies a signed token and returns the raw token part.
func VerifyToken(secret []byte, signed string) (string, error) {
	parts := strings.SplitN(signed, ".", 2)
	if len(parts) != 2 {
		return "", ErrInvalidToken
	}
	token, sig := parts[0], parts[1]
	expected := sign(secret, token)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", ErrInvalidToken
	}
	return token, nil
}

func sign(secret []byte, token string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// SetCookie writes the signed session cookie.
func SetCookie(w http.ResponseWriter, signed string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(90 * 24 * time.Hour),
	})
}

// ClearCookie removes the session cookie.
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// TokenFromRequest extracts and verifies the session cookie.
func TokenFromRequest(r *http.Request, secret []byte) (string, error) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", ErrInvalidToken
	}
	return VerifyToken(secret, c.Value)
}
