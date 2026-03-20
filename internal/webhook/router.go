package webhook

import "github.com/gin-gonic/gin"

func RegisterRoutes(router gin.IRouter, cfg Config) {
	handler := cfg.Handler
	if handler == nil {
		handler = NewLogHandler(nil)
	}
	receiver := &Receiver{
		verifier: NewSignatureVerifier(cfg.Secret),
		parser:   NewParser(),
		handler:  handler,
	}
	router.POST("/webhooks/gitea", receiver.Handle)
}
