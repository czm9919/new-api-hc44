package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// API 请求签名防重放（v1.1.0 M1 · B144）。
//
// 管理类写接口接受 access token（PAT/legacy token）认证时，客户端可以额外
// 携带三个签名头，让服务端验证「这条请求确实由持有 token 的客户端发起、
// 且只出现一次」：
//
//	X-Timestamp:  Unix 秒级时间戳，与服务端偏差超过窗口即拒绝
//	X-Nonce:      每次请求唯一的随机串（16~128 字节可打印 ASCII），窗口内去重
//	X-Signature:  hex(HMAC-SHA256(key, canonical))
//
// canonical 串（\n 连接）：
//
//	method \n path \n sha256(body) hex \n timestamp \n nonce
//
// HMAC 密钥由 access token 明文派生（sha256("new-api:request-signature:v1:"+token)），
// 与导出水印/secret-cipher 同风格、不同盐，避免多用途密钥流共享；不新增任何
// 密钥管理面：token 即签名密钥，token 换了签名密钥自然跟着换。
//
// 威胁模型：防重放（抓包重放同一条写请求）与防篡改（改 body/路径/方法即验签
// 失败）。签名随 token 而非浏览器会话——浏览器 session 登录不带 Authorization
// 头，走 session 豁免路径，由 CSRFOriginCheck 保护同源。
//
// 兼容策略：未带签名头的旧客户端在 APISignatureRequired=false（默认）时放行；
// 一旦带了签名头，无论开关如何都严格校验（客户端主动启用即获得防重放保护）。
// APISignatureRequired=true 时，token 认证的管理写请求必须携带签名。

const (
	// RequestSignatureHeader carries the hex HMAC-SHA256 signature.
	RequestSignatureHeader = "X-Signature"
	// RequestTimestampHeader carries the Unix second timestamp used in the canonical string.
	RequestTimestampHeader = "X-Timestamp"
	// RequestNonceHeader carries the per-request unique nonce.
	RequestNonceHeader = "X-Nonce"

	requestSignatureKeySalt = "new-api:request-signature:v1:"
)

// requestSignatureWindow returns the allowed |server time - X-Timestamp| skew
// in seconds. A misconfigured window falls back to the default so the feature
// can never be silently disabled by an option typo.
func requestSignatureWindow() int64 {
	window := int64(common.APISignatureWindowSeconds)
	if window <= 0 {
		window = 300
	}
	return window
}

// RequestSignatureKey derives the HMAC key for one access token. Exported so
// SDK authors and tests sign with exactly the same derivation the server uses.
func RequestSignatureKey(rawToken string) []byte {
	key := sha256.Sum256([]byte(requestSignatureKeySalt + rawToken))
	return key[:]
}

// RequestSignatureBodyHash returns the hex sha256 of the request body as used
// in the canonical string. An empty body is hashed like any other body.
func RequestSignatureBodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// RequestSignatureCanonicalString is the exact byte string the client signs
// and the server re-computes. Exported for SDK authors.
func RequestSignatureCanonicalString(method, path, bodyHash string, timestamp int64, nonce string) string {
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		bodyHash,
		strconv.FormatInt(timestamp, 10),
		nonce,
	}, "\n")
}

// SignRequestSignature computes the X-Signature value for a request. Exported
// for SDK authors and tests.
func SignRequestSignature(rawToken, method, path string, body []byte, timestamp int64, nonce string) string {
	canonical := RequestSignatureCanonicalString(method, path, RequestSignatureBodyHash(body), timestamp, nonce)
	mac := hmac.New(sha256.New, RequestSignatureKey(rawToken))
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// signatureNonceSeen remembers nonces already accepted, keyed by nonce with
// the expiry unix second as value (login_guard 风格的内存去重)。
var signatureNonceSeen sync.Map

var signatureNonceCleanerOnce sync.Once

func startSignatureNonceCleaner() {
	signatureNonceCleanerOnce.Do(func() {
		go func() {
			for range time.Tick(time.Minute) {
				now := time.Now().Unix()
				signatureNonceSeen.Range(func(key, value any) bool {
					expiry, _ := value.(int64)
					if expiry > 0 && expiry < now {
						signatureNonceSeen.Delete(key)
					}
					return true
				})
			}
		}()
	})
}

var errRequestSignature = errors.New("request signature")

func requestSignatureReject(c *gin.Context, code, reason string) {
	// 拒绝原因进审计参数，方便在安全审计里区分「重放 / 过期 / 篡改」。
	setAccessTokenAuditParam(c, "signature_failure", reason)
	abortWithWebMessage(c, http.StatusUnauthorized, code, common.NewMessage("API request signature is invalid: "+reason))
}

// enforceRequestSignature validates the anti-replay signature headers of
// access-token authenticated admin write requests. Called from authHelper
// AFTER authentication, so the raw token is known valid and the caller is an
// admin. Returns false when the request was rejected.
//
// Exemptions (documented in the file header): session-authenticated requests
// carry no Authorization header; non-write methods (GET/HEAD/OPTIONS) carry no
// body to protect. Both pass through untouched.
func enforceRequestSignature(c *gin.Context) bool {
	switch c.Request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return true
	}
	raw, hasToken := authorizationToken(c.GetHeader("Authorization"))
	if !hasToken {
		// 浏览器 session / 匿名请求：无 token 即无签名密钥，豁免。
		return true
	}
	signature := strings.TrimSpace(c.GetHeader(RequestSignatureHeader))
	timestamp := strings.TrimSpace(c.GetHeader(RequestTimestampHeader))
	nonce := strings.TrimSpace(c.GetHeader(RequestNonceHeader))
	if signature == "" && timestamp == "" && nonce == "" {
		if !common.APISignatureRequired {
			// 旧客户端兼容：默认放行未签名的请求。
			return true
		}
		requestSignatureReject(c, "REQUEST_SIGNATURE_REQUIRED", "missing_signature")
		return false
	}
	startSignatureNonceCleaner()
	if err := verifyRequestSignature(c, raw, signature, timestamp, nonce); err != nil {
		requestSignatureReject(c, "REQUEST_SIGNATURE_INVALID", err.Error())
		return false
	}
	return true
}

func verifyRequestSignature(c *gin.Context, rawToken, signature, timestamp, nonce string) error {
	if signature == "" || timestamp == "" || nonce == "" {
		return errors.New("incomplete signature headers")
	}
	stamp, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || stamp <= 0 {
		return errors.New("malformed_timestamp")
	}
	window := requestSignatureWindow()
	skew := common.GetTimestamp() - stamp
	if skew < 0 {
		skew = -skew
	}
	if skew > window {
		return errors.New("timestamp_outside_window")
	}
	// nonce 必须是不可打印控制字符之外的 ASCII，且长度在 16~128 之间，
	// 避免空 nonce / 巨型 nonce 打爆内存去重表。
	if len(nonce) < 16 || len(nonce) > 128 {
		return errors.New("malformed_nonce")
	}
	for i := 0; i < len(nonce); i++ {
		if nonce[i] < 0x21 || nonce[i] > 0x7e {
			return errors.New("malformed_nonce")
		}
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return errors.New("unreadable_body")
	}
	// 回填 body，后续 handler 的 ShouldBindJSON 等照常工作。
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size {
		return errors.New("malformed_signature")
	}
	canonical := RequestSignatureCanonicalString(c.Request.Method, c.Request.URL.Path, RequestSignatureBodyHash(body), stamp, nonce)
	mac := hmac.New(sha256.New, RequestSignatureKey(rawToken))
	mac.Write([]byte(canonical))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return errors.New("signature_mismatch")
	}
	// 验签通过后才占用 nonce：验证失败的请求不消耗对方的 nonce 空间，
	// 攻击者也无法用重放之外的 nonce 预占（LoadOrStore 保证原子性）。
	if _, loaded := signatureNonceSeen.LoadOrStore(nonce, common.GetTimestamp()+window); loaded {
		return errors.New("nonce_replayed")
	}
	return nil
}
