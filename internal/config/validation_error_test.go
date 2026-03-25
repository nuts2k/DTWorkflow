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

func TestValidationError_Unwrap_Nil(t *testing.T) {
	var ve *ValidationError
	errList := ve.Unwrap()
	if errList != nil {
		t.Errorf("nil receiver Unwrap 应返回 nil，得到: %v", errList)
	}
}

func TestValidationError_Is(t *testing.T) {
	ve := &ValidationError{Errors: []error{errors.New("test")}}

	if !errors.Is(ve, ErrInvalidConfig) {
		t.Error("ValidationError 应匹配 ErrInvalidConfig")
	}

	if errors.Is(ve, errors.New("other")) {
		t.Error("ValidationError 不应匹配其他错误")
	}
}

func TestValidationError_Error_Empty(t *testing.T) {
	ve := &ValidationError{}
	msg := ve.Error()
	if msg != "配置校验失败" {
		t.Errorf("空 Errors 的 Error() = %q, want %q", msg, "配置校验失败")
	}
}

func TestValidationError_Error_Nil(t *testing.T) {
	var ve *ValidationError
	msg := ve.Error()
	if msg != "配置校验失败" {
		t.Errorf("nil receiver Error() = %q, want %q", msg, "配置校验失败")
	}
}
