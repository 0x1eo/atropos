package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"atropos/engine"
	"atropos/internal/logger"
)

type CutRequest struct {
	Node      string  `json:"node" binding:"required"`
	Entropy   float64 `json:"entropy" binding:"required,gte=0,lte=1"`
	Timestamp string  `json:"timestamp"`
}

type CutResponse struct {
	Node      string `json:"node"`
	Action    string `json:"action"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
}

type WebhookHandler struct {
	executor   *engine.Executor
	hmacSecret []byte
}

func NewWebhookHandler(exec *engine.Executor, hmacSecret string) *WebhookHandler {
	return &WebhookHandler{
		executor:   exec,
		hmacSecret: []byte(hmacSecret),
	}
}

func (h *WebhookHandler) RegisterRoutes(r *gin.Engine) {
	r.Use(gin.Recovery())

	api := r.Group("/api/v1")
	{
		api.POST("/cut", h.hmacMiddleware(), h.handleCut)
		api.GET("/health", h.handleHealth)
	}
}

func (h *WebhookHandler) handleCut(c *gin.Context) {
	var req CutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	logger.WebhookReceived(req.Node, req.Entropy, true)

	resultCh := h.executor.ExecuteCutAsync(c.Request.Context(), req.Node, req.Entropy)

	select {
	case result := <-resultCh:
		resp := CutResponse{
			Node:      result.Target,
			Action:    result.Action,
			Success:   result.Success,
			LatencyMs: result.LatencyMs,
		}
		if result.Error != nil {
			resp.Error = result.Error.Error()
		}

		if result.Success {
			c.JSON(http.StatusOK, resp)
		} else {
			c.JSON(http.StatusInternalServerError, resp)
		}

	case <-time.After(35 * time.Second):
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"error": "cut operation timed out",
			"node":  req.Node,
		})
	}
}

func (h *WebhookHandler) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "operational",
		"service": "atropos",
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *WebhookHandler) hmacMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		sig := c.GetHeader("X-Lachesis-Signature")
		if sig == "" {
			logger.WebhookReceived("unknown", 0, false)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing signature"})
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
			return
		}

		if !h.verifySignature(body, sig) {
			logger.WebhookReceived("unknown", 0, false)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid signature"})
			return
		}

		c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
		c.Next()
	}
}

func (h *WebhookHandler) verifySignature(payload []byte, signature string) bool {
	if len(h.hmacSecret) == 0 {
		return true
	}

	parts := strings.SplitN(signature, "=", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return false
	}

	expectedMAC, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, h.hmacSecret)
	mac.Write(payload)
	return hmac.Equal(mac.Sum(nil), expectedMAC)
}

func NewServer(exec *engine.Executor, hmacSecret string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{"/api/v1/health"},
	}))

	handler := NewWebhookHandler(exec, hmacSecret)
	handler.RegisterRoutes(r)

	return r
}
