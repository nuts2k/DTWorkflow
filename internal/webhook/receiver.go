package webhook

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	headerEventType  = "X-Gitea-Event"
	headerDeliveryID = "X-Gitea-Delivery"
	headerSignature  = "X-Gitea-Signature"

	// 固定限制 webhook 请求体大小，避免内存占用过高。
	maxRequestBodySize = 1 << 20 // 1MiB
)

type Config struct {
	Secret  string
	Handler Handler
}

type Receiver struct {
	verifier *SignatureVerifier
	parser   *Parser
	handler  Handler
}

func (r *Receiver) Handle(c *gin.Context) {
	// 先做请求体大小限制：超限统一返回 400（不走签名校验等后续逻辑）。
	limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)
	body, err := io.ReadAll(limitedBody)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		c.Status(http.StatusBadRequest)
		return
	}
	if ct := c.GetHeader("Content-Type"); ct == "" || !strings.HasPrefix(ct, "application/json") {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := r.verifier.Verify(c.GetHeader(headerSignature), body); err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	event, err := r.parser.Parse(c.GetHeader(headerEventType), c.GetHeader(headerDeliveryID), body)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnsupportedEvent), errors.Is(err, ErrUnsupportedAction):
			c.Status(http.StatusOK)
		case errors.Is(err, ErrInvalidPayload):
			c.Status(http.StatusBadRequest)
		default:
			c.Status(http.StatusInternalServerError)
		}
		return
	}
	switch e := event.(type) {
	case PullRequestEvent:
		err = r.handler.HandlePullRequest(c.Request.Context(), e)
	case IssueLabelEvent:
		err = r.handler.HandleIssueLabel(c.Request.Context(), e)
	}
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}
