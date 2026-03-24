package config

import (
	"errors"
	"testing"
)

func TestValidationError_Unwrap(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Webhook.Secret = ""
	cfg.Worker.Concurrency = 0

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}

	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}

	errList := ve.Unwrap()
	if len(errList) < 2 {
		t.Fatalf("Unwrap length = %d, want >= %d", len(errList), 2)
	}

	if errList[0] == nil {
		t.Fatalf("Unwrap[0] is nil")
	}
}
