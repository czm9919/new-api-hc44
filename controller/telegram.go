package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
)

// TelegramLegacyAuth retires the unsigned-flow widget endpoints. Existing
// Telegram bindings are used by the unified OAuth provider instead.
func TelegramLegacyAuth(c *gin.Context) {
	common.ApiErrorMsgStatus(c, http.StatusGone, "Telegram login has changed. Reload the page and start Telegram OAuth again.", gin.H{"code": "TELEGRAM_LEGACY_AUTH_REMOVED"})
}
