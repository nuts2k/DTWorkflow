package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrMissingSignature    = errors.New("missing signature")
	ErrSignatureMismatch   = errors.New("signature mismatch")
	ErrSecretNotConfigured = errors.New("secret not configured")
)

type SignatureVerifier struct {
	secret []byte
}

func NewSignatureVerifier(secret string) *SignatureVerifier {
	return &SignatureVerifier{secret: []byte(secret)}
}

func (v *SignatureVerifier) Verify(signature string, body []byte) error {
	if len(v.secret) == 0 {
		return ErrSecretNotConfigured
	}
	if signature == "" {
		return ErrMissingSignature
	}

	// 兼容两种签名格式：
	// 1) Gitea 原生 raw hex
	// 2) sha256=<hex>
	sigHex := signature
	if strings.HasPrefix(signature, "sha256=") {
		sigHex = strings.TrimPrefix(signature, "sha256=")
	}

	provided, err := hex.DecodeString(sigHex)
	if err != nil {
		return ErrSignatureMismatch
	}
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	if !hmac.Equal(provided, expected) {
		return ErrSignatureMismatch
	}
	return nil
}
