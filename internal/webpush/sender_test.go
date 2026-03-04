package webpush_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	webpushlib "github.com/SherClockHolmes/webpush-go"
	"github.com/emilhauk/msg/internal/webpush"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testVAPIDKeys generates a fresh VAPID key pair for the test.
// GenerateVAPIDKeys returns (privateKey, publicKey, err).
func testVAPIDKeys(t *testing.T) (pub, priv string) {
	t.Helper()
	priv, pub, err := webpushlib.GenerateVAPIDKeys()
	require.NoError(t, err)
	return pub, priv
}

// testSubscriptionJSON builds a Web Push subscription JSON string pointing at
// the given endpoint. It uses a freshly generated P-256 ECDH key pair so that
// webpush-go can actually encrypt the payload.
func testSubscriptionJSON(t *testing.T, endpoint string) string {
	t.Helper()

	// Generate a real P-256 ECDH key pair (required so the library can encrypt).
	privKey, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	p256dh := base64.RawURLEncoding.EncodeToString(privKey.PublicKey().Bytes())

	// 16-byte auth secret.
	authBytes := make([]byte, 16)
	_, err = rand.Read(authBytes)
	require.NoError(t, err)
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	sub, err := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{
			"p256dh": p256dh,
			"auth":   auth,
		},
	})
	require.NoError(t, err)
	return string(sub)
}

func newSender(t *testing.T) *webpush.Sender {
	t.Helper()
	pub, priv := testVAPIDKeys(t)
	return webpush.New(webpush.Config{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		VAPIDSubject:    "mailto:test@test.com",
	})
}

func TestSend_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sender := newSender(t)
	subJSON := testSubscriptionJSON(t, srv.URL)

	expired, err := sender.Send(context.Background(), subJSON, webpush.Payload{Title: "test", Body: "body"})
	require.NoError(t, err)
	assert.False(t, expired)
}

func TestSend_Gone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	sender := newSender(t)
	subJSON := testSubscriptionJSON(t, srv.URL)

	expired, err := sender.Send(context.Background(), subJSON, webpush.Payload{Title: "test", Body: "body"})
	require.NoError(t, err)
	assert.True(t, expired)
}

func TestSend_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sender := newSender(t)
	subJSON := testSubscriptionJSON(t, srv.URL)

	// non-2xx responses are now logged and returned as errors so APNs rejections are visible.
	expired, err := sender.Send(context.Background(), subJSON, webpush.Payload{Title: "test", Body: "body"})
	assert.Error(t, err)
	assert.False(t, expired)
}

func TestSend_InvalidJSON(t *testing.T) {
	sender := newSender(t)

	expired, err := sender.Send(context.Background(), "not-valid-json", webpush.Payload{Title: "test"})
	assert.Error(t, err)
	assert.False(t, expired)
}

func TestSendToMany_ReturnsExpired(t *testing.T) {
	// sub1 → 201 Created (still valid).
	sub1Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer sub1Srv.Close()

	// sub2 → 410 Gone (subscription expired).
	sub2Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer sub2Srv.Close()

	sender := newSender(t)
	subs := map[string]string{
		sub1Srv.URL: testSubscriptionJSON(t, sub1Srv.URL),
		sub2Srv.URL: testSubscriptionJSON(t, sub2Srv.URL),
	}

	expired := sender.SendToMany(context.Background(), subs, webpush.Payload{Title: "test", Body: "body"})
	require.Len(t, expired, 1)
	assert.Equal(t, sub2Srv.URL, expired[0])
}
