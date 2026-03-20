package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type stubHandler struct {
	pulls  int
	issues int
	err    error
}

func (h *stubHandler) HandlePullRequest(_ context.Context, _ PullRequestEvent) error {
	h.pulls++
	return h.err
}

func (h *stubHandler) HandleIssueLabel(_ context.Context, _ IssueLabelEvent) error {
	h.issues++
	return h.err
}

func TestReceiver_HandlePullRequestOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":"opened","repository":{"full_name":"owner/repo","owner":{"login":"owner"},"name":"repo"},"pull_request":{"number":42}}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-1")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if h.pulls != 1 {
		t.Fatalf("pull handler calls = %d, want 1", h.pulls)
	}
}

func TestReceiver_HandleSignatureMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: &stubHandler{}})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(`{"action":"opened"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-bad")
	req.Header.Set(headerSignature, "sha256=deadbeef")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
}

func TestReceiver_HandleUnsupportedEventReturnsOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: &stubHandler{}})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "push")
	req.Header.Set(headerDeliveryID, "delivery-ignore")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
}

func TestReceiver_HandleHandlerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":"opened","repository":{"full_name":"owner/repo","owner":{"login":"owner"},"name":"repo"},"pull_request":{"number":42}}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{err: errors.New("boom")}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-err")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusInternalServerError)
	}
}

func TestReceiver_HandleMissingSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: &stubHandler{}})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(`{"action":"opened"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-no-signature")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
}

func TestReceiver_HandlePayloadTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	body := strings.Repeat("a", maxRequestBodySize+1)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-large")
	req.Header.Set(headerSignature, "deadbeef")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
	if h.pulls != 0 || h.issues != 0 {
		t.Fatalf("handler calls = pulls:%d issues:%d, want 0", h.pulls, h.issues)
	}
}

func TestReceiver_HandlePayloadAtLimitOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prefix := `{"action":"opened","repository":{"full_name":"owner/repo","owner":{"login":"owner"},"name":"repo"},"pull_request":{"number":42},"padding":"`
	suffix := `"}`
	paddingSize := maxRequestBodySize - len(prefix) - len(suffix)
	if paddingSize <= 0 {
		t.Fatalf("padding size = %d, want > 0", paddingSize)
	}
	body := prefix + strings.Repeat("a", paddingSize) + suffix

	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-limit")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if h.pulls != 1 {
		t.Fatalf("pull handler calls = %d, want 1", h.pulls)
	}
}

func TestReceiver_HandleIssueLabelOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":"labeled","repository":{"full_name":"owner/repo","owner":{"login":"owner"},"name":"repo"},"issue":{"number":7},"label":{"name":"auto-fix","color":"ff0000"}}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "issues")
	req.Header.Set(headerDeliveryID, "delivery-issue")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if h.issues != 1 {
		t.Fatalf("issue handler calls = %d, want 1", h.issues)
	}
}

func TestReceiver_HandlePullRequestSynchronizedOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":"synchronized","repository":{"full_name":"owner/repo","owner":{"login":"owner"},"name":"repo"},"pull_request":{"number":42}}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-sync")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if h.pulls != 1 {
		t.Fatalf("pull handler calls = %d, want 1", h.pulls)
	}
}

func TestReceiver_HandleInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-invalid-json")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
	if h.pulls != 0 || h.issues != 0 {
		t.Fatalf("handler calls = pulls:%d issues:%d, want 0", h.pulls, h.issues)
	}
}

func TestReceiver_HandleEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-empty-body")
	req.Header.Set(headerSignature, "deadbeef")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
	if h.pulls != 0 || h.issues != 0 {
		t.Fatalf("handler calls = pulls:%d issues:%d, want 0", h.pulls, h.issues)
	}
}

func TestReceiver_HandleNonJSONContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":"opened"}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-non-json")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
	if h.pulls != 0 || h.issues != 0 {
		t.Fatalf("handler calls = pulls:%d issues:%d, want 0", h.pulls, h.issues)
	}
}

func TestReceiver_HandleUnsupportedActionReturnsOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":"closed","repository":{"full_name":"owner/repo","owner":{"login":"owner"},"name":"repo"},"pull_request":{"number":42}}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "pull_request")
	req.Header.Set(headerDeliveryID, "delivery-closed")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if h.pulls != 0 || h.issues != 0 {
		t.Fatalf("handler calls = pulls:%d issues:%d, want 0", h.pulls, h.issues)
	}
}

func TestReceiver_HandleIssueUnlabeledOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"action":"unlabeled","repository":{"full_name":"owner/repo","owner":{"login":"owner"},"name":"repo"},"issue":{"number":7},"label":{"name":"auto-fix","color":"ff0000"}}`
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	h := &stubHandler{}
	router := gin.New()
	RegisterRoutes(router, Config{Secret: "secret", Handler: h})

	req := httptest.NewRequest(http.MethodPost, "/webhooks/gitea", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerEventType, "issues")
	req.Header.Set(headerDeliveryID, "delivery-issue-unlabeled")
	req.Header.Set(headerSignature, signature)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}
	if h.issues != 1 {
		t.Fatalf("issue handler calls = %d, want 1", h.issues)
	}
}
