package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
)

// 异常登录提醒（v1.1.0 M1 · B05）。
//
// 判定口径：本次成功登录的 IP 从未出现在该账号最近 500 条成功登录的审计
// 记录里，且账号已有登录历史（首次登录不提醒，否则每个新注册账号都会被
// 「新设备」打扰一次）。同一新 IP 只提醒一次——第一次登录后审计里就有了
// 这条 IP，第二次登录自然变成「已知」。
//
// 判定必须在 recordLoginAudit 写入本次登录之前做，否则本次 IP 永远是
// 「已知」；邮件发送则异步进行，不拖慢登录响应。

// NotifyUnrecognizedLogin checks whether ip is new for this account and, if
// so, emails the account owner. The DB check is synchronous (one indexed
// query) so it can run before the login audit row is written; delivery runs
// in a background goroutine and never blocks the login response.
func NotifyUnrecognizedLogin(userId int, method, ip string) {
	if userId <= 0 || ip == "" {
		return
	}
	knownIP, hasHistory, err := model.KnownLoginIP(userId, ip)
	if err != nil {
		common.SysLog(common.LogText("login alert check failed for user %d: %v", userId, err))
		return
	}
	if !hasHistory || knownIP {
		return
	}
	go sendUnrecognizedLoginEmail(userId, method, ip)
}

func sendUnrecognizedLoginEmail(userId int, method, ip string) {
	defer func() {
		if r := recover(); r != nil {
			common.SysLog(common.LogText("login alert email panic for user %d: %v", userId, r))
		}
	}()
	var user model.User
	if err := model.DB.Select("id", "username", "email").
		Where("id = ?", userId).First(&user).Error; err != nil {
		return
	}
	if user.Email == "" {
		return
	}
	lang := model.GetUserLanguage(userId)
	args := map[string]any{
		"SystemName": common.SystemName,
		"Username":   user.Username,
		"Time":       time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		"IP":         ip,
		"Method":     method,
	}
	subject := i18n.TranslateCN(lang, i18n.MsgEmailLoginAlertSubject, args)
	content := fmt.Sprintf("<p>%s</p>", i18n.TranslateCN(lang, i18n.MsgEmailLoginAlertBody, args))
	if err := common.SendEmail(subject, user.Email, content); err != nil {
		common.SysLog(common.LogText("login alert email to user %d (%s) failed: %v", userId, ip, err))
	} else {
		common.SysLog(common.LogText("login alert email sent to user %d for new ip %s", userId, ip))
	}
}
