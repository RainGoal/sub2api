package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Codex OAuth endpoints strip output caps and add default instructions. Do not
// send a low-cost monitor request through a protocol that cannot retain its cap.
func rejectUnboundedMonitorCodex(ctx context.Context, c *gin.Context, account *Account) error {
	if !IsModelPlazaMonitorProbe(ctx) || !account.UsesOpenAICodexProtocol() {
		return nil
	}
	err := errors.New("low-budget monitoring is unsupported by the selected upstream protocol")
	if c != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"type": "invalid_request_error", "message": err.Error(),
		}})
	}
	return err
}
