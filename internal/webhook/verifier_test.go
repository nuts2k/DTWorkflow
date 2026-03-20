package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func TestSignatureVerifier_VerifyRawHex(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	verifier := NewSignatureVerifier("secret")
	if err := verifier.Verify(signature, body); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestSignatureVerifier_VerifySHA256Prefix(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	verifier := NewSignatureVerifier("secret")
	if err := verifier.Verify(signature, body); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestSignatureVerifier_VerifyMismatch(t *testing.T) {
	verifier := NewSignatureVerifier("secret")
	if err := verifier.Verify("sha256=deadbeef", []byte(`{}`)); err == nil {
		t.Fatal("Verify() should fail for mismatched signature")
	}
}

func TestSignatureVerifier_VerifyInvalidHex(t *testing.T) {
	verifier := NewSignatureVerifier("secret")
	if err := verifier.Verify("not-hex", []byte(`{}`)); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrSignatureMismatch)
	}
}

func TestSignatureVerifier_VerifyInvalidHexWithSHA256Prefix(t *testing.T) {
	verifier := NewSignatureVerifier("secret")
	if err := verifier.Verify("sha256=not-hex", []byte(`{}`)); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrSignatureMismatch)
	}
}

func TestSignatureVerifier_VerifySecretNotConfigured(t *testing.T) {
	verifier := NewSignatureVerifier("")
	if err := verifier.Verify("deadbeef", []byte(`{}`)); !errors.Is(err, ErrSecretNotConfigured) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrSecretNotConfigured)
	}
}

func TestSignatureVerifier_VerifyMissingSignature(t *testing.T) {
	verifier := NewSignatureVerifier("secret")
	if err := verifier.Verify("", []byte(`{}`)); !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrMissingSignature)
	}
}
