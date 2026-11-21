package common

import "regexp"

// 日志脱敏（v1.1.0 M1 · B142）。
//
// 日志是长期留存、常被导出、常被交给他人的数据。渠道密钥、上游 token、
// 用户邮箱、手机号一旦进了日志文件，就等于绕过了数据库里的加密与访问控制。
// 这里在「写日志」这唯一出口统一遮盖，而不是要求每个调用点记得自己遮盖。
//
// 遮盖强度按「还能不能反推」来定：密钥整体替换为 ***；邮箱只保留首字符与
// 域名（够定位是哪个用户，无法作为联系方式使用）；手机号保留前 3 后 4 位
// （与运营商账单口径一致）。
//
// 边界：本函数只处理文本形态的敏感值。结构化字段（如 channel.Key）如果要
// 写日志，调用方应直接省略或用 common.MaskSecret 先行处理。
var (
	// Authorization: Bearer <token>
	redactBearerRe = regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9\-._~+/]{8,}=*`)
	// 各家 SDK 的 sk- 前缀密钥（含 sk-proj-、sk-ant- 等变体）。
	redactSkKeyRe = regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{6,}`)
	// key=value 与 JSON 的 "key": "value" 两种形态。值至少 3 个字符，
	// 避免把 "password: no" 这类说明文字也吃掉。
	redactKVRe = regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|access[_-]?token|refresh[_-]?token|id[_-]?token|token|secret|client[_-]?secret|password|passwd|pwd|cookie|authorization)(\s*[:=]\s*|"\s*:\s*")([^\s"',;&}\]]{3,})`)
	// 邮箱：保留首字符与完整域名。
	redactEmailRe = regexp.MustCompile(`([A-Za-z0-9._%+\-])[A-Za-z0-9._%+\-]*@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	// 中国大陆手机号：保留前 3 与后 4 位。
	redactPhoneRe = regexp.MustCompile(`\b(1[3-9]\d)\d{4}(\d{4})\b`)
)

// RedactSensitive masks credentials, email addresses and phone numbers in a
// log line. It is safe to call on already-redacted text.
func RedactSensitive(s string) string {
	if s == "" {
		return s
	}
	s = redactBearerRe.ReplaceAllString(s, "$1 ***")
	s = redactSkKeyRe.ReplaceAllString(s, "sk-***")
	s = redactKVRe.ReplaceAllString(s, "$1$2***")
	s = redactEmailRe.ReplaceAllString(s, "$1***@$2")
	s = redactPhoneRe.ReplaceAllString(s, "$1****$2")
	return s
}

// MaskSecret keeps a short prefix so an operator can tell two keys apart when
// assembling a support request, and nothing more.
func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "***"
	}
	return secret[:4] + "***" + secret[len(secret)-2:]
}
