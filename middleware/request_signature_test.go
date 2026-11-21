package middleware

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B144 测试基建：一个由 access token（legacy 形态，跳过路由声明校验）认证的
// 管理员账号 + 一条 POST 管理写路由。
func setupRequestSignatureTest(t *testing.T) (*model.User, string, *gin.Engine) {
	t.Helper()
	setupDashboardAuthMiddlewareTest(t)
	gin.SetMode(gin.TestMode)
	raw := "signature-admin-legacy-token"
	user := createMiddlewarePATUser(t, "signature-admin", raw)
	require.NoError(t, model.DB.Model(user).Update("role", common.RoleAdminUser).Error)
	user.Role = common.RoleAdminUser

	router := gin.New()
	route := router.Group("/api")
	route.Use(AdminAuth())
	route.POST("/signed", func(c *gin.Context) {
		var body struct {
			Amount int `json:"amount"`
		}
		// 验签中间件必须把 body 回填，handler 才能正常绑定。
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "body_unreadable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "id": c.GetInt("id"), "amount": body.Amount})
	})
	route.GET("/signed", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "id": c.GetInt("id")})
	})
	t.Cleanup(func() {
		common.APISignatureRequired = false
		common.APISignatureWindowSeconds = 300
	})
	return user, raw, router
}

type signedRequest struct {
	method    string
	path      string
	body      []byte
	token     string
	timestamp int64
	nonce     string
}

func newSignedRequest(t *testing.T, spec signedRequest) *http.Request {
	t.Helper()
	request := httptest.NewRequest(spec.method, spec.path, bytes.NewReader(spec.body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+spec.token)
	request.Header.Set(RequestTimestampHeader, strconv.FormatInt(spec.timestamp, 10))
	request.Header.Set(RequestNonceHeader, spec.nonce)
	request.Header.Set(RequestSignatureHeader, SignRequestSignature(
		spec.token, spec.method, spec.path, spec.body, spec.timestamp, spec.nonce,
	))
	return request
}

func freshNonce() string {
	nonce, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return nonce
}

func TestRequestSignatureAcceptsSignedAdminWrite(t *testing.T) {
	user, raw, router := setupRequestSignatureTest(t)
	body := []byte(`{"amount":7}`)
	request := newSignedRequest(t, signedRequest{
		method: http.MethodPost, path: "/api/signed", body: body,
		token: raw, timestamp: common.GetTimestamp(), nonce: freshNonce(),
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Success bool `json:"success"`
		ID      int  `json:"id"`
		Amount  int  `json:"amount"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	assert.True(t, payload.Success)
	assert.Equal(t, user.Id, payload.ID, "body must survive verification and authentication")
	assert.Equal(t, 7, payload.Amount)
}

func TestRequestSignatureRejectsTamperedBody(t *testing.T) {
	_, raw, router := setupRequestSignatureTest(t)
	signedBody := []byte(`{"amount":7}`)
	timestamp := common.GetTimestamp()
	nonce := freshNonce()
	// 用原始 body 签名，发送时换成篡改后的 body。
	request := httptest.NewRequest(http.MethodPost, "/api/signed", bytes.NewReader([]byte(`{"amount":700000}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+raw)
	request.Header.Set(RequestTimestampHeader, strconv.FormatInt(timestamp, 10))
	request.Header.Set(RequestNonceHeader, nonce)
	request.Header.Set(RequestSignatureHeader, SignRequestSignature(raw, http.MethodPost, "/api/signed", signedBody, timestamp, nonce))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "REQUEST_SIGNATURE_INVALID")
	assert.Contains(t, response.Body.String(), "signature_mismatch")
}

func TestRequestSignatureRejectsReplay(t *testing.T) {
	_, raw, router := setupRequestSignatureTest(t)
	body := []byte(`{"amount":7}`)
	timestamp := common.GetTimestamp()
	nonce := freshNonce()

	first := httptest.NewRecorder()
	router.ServeHTTP(first, newSignedRequest(t, signedRequest{
		method: http.MethodPost, path: "/api/signed", body: body,
		token: raw, timestamp: timestamp, nonce: nonce,
	}))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	// 同 nonce 同签名的第二次请求必须被拒（防重放的核心）。
	second := httptest.NewRecorder()
	router.ServeHTTP(second, newSignedRequest(t, signedRequest{
		method: http.MethodPost, path: "/api/signed", body: body,
		token: raw, timestamp: timestamp, nonce: nonce,
	}))
	require.Equal(t, http.StatusUnauthorized, second.Code, second.Body.String())
	assert.Contains(t, second.Body.String(), "nonce_replayed")
}

func TestRequestSignatureRejectsStaleTimestamp(t *testing.T) {
	_, raw, router := setupRequestSignatureTest(t)
	common.APISignatureWindowSeconds = 60
	body := []byte(`{"amount":7}`)
	request := newSignedRequest(t, signedRequest{
		method: http.MethodPost, path: "/api/signed", body: body,
		token: raw, timestamp: common.GetTimestamp() - 61, nonce: freshNonce(),
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "timestamp_outside_window")

	// 窗口内的同一形态请求放行，确认拒绝原因是时间戳而不是别的。
	fresh := htptRecorder(router, newSignedRequest(t, signedRequest{
		method: http.MethodPost, path: "/api/signed", body: body,
		token: raw, timestamp: common.GetTimestamp() - 30, nonce: freshNonce(),
	}))
	assert.Equal(t, http.StatusOK, fresh.Code, fresh.Body.String())
}

func htptRecorder(router *gin.Engine, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestRequestSignatureOptionalByDefault(t *testing.T) {
	_, raw, router := setupRequestSignatureTest(t)
	request := httptest.NewRequest(http.MethodPost, "/api/signed", bytes.NewReader([]byte(`{"amount":7}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+raw)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	// 未强制时旧客户端照旧可用。
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func TestRequestSignatureRequiredRejectsUnsigned(t *testing.T) {
	_, raw, router := setupRequestSignatureTest(t)
	common.APISignatureRequired = true
	request := httptest.NewRequest(http.MethodPost, "/api/signed", bytes.NewReader([]byte(`{"amount":7}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+raw)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "REQUEST_SIGNATURE_REQUIRED")
}

func TestRequestSignatureSkipsNonWriteMethodsAndSessions(t *testing.T) {
	_, raw, router := setupRequestSignatureTest(t)
	common.APISignatureRequired = true

	// GET 没有 body 可篡改，强制开关也不要求签名。
	get := httptest.NewRequest(http.MethodGet, "/api/signed", nil)
	get.Header.Set("Authorization", "Bearer "+raw)
	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, get)
	assert.Equal(t, http.StatusOK, getResponse.Code, getResponse.Body.String())

	// 无 Authorization（浏览器 session/匿名）不参与验签：由鉴权层给出 AUTH_* 结论，
	// 而不是 REQUEST_SIGNATURE_REQUIRED。
	anonymous := httptest.NewRequest(http.MethodPost, "/api/signed", bytes.NewReader([]byte(`{"amount":7}`)))
	anonymous.Header.Set("Content-Type", "application/json")
	anonymousResponse := httptest.NewRecorder()
	router.ServeHTTP(anonymousResponse, anonymous)
	assert.Equal(t, http.StatusUnauthorized, anonymousResponse.Code)
	assert.NotContains(t, anonymousResponse.Body.String(), "REQUEST_SIGNATURE_REQUIRED")
	assert.Contains(t, anonymousResponse.Body.String(), "AUTH_")
}

func TestRequestSignatureRejectsMalformedHeaders(t *testing.T) {
	_, raw, router := setupRequestSignatureTest(t)
	body := []byte(`{"amount":7}`)
	cases := []struct {
		name      string
		timestamp string
		nonce     string
		signature string
		want      string
	}{
		{name: "non numeric timestamp", timestamp: "not-a-time", nonce: freshNonce(), signature: strings.Repeat("a", 64), want: "malformed_timestamp"},
		{name: "short nonce", timestamp: strconv.FormatInt(common.GetTimestamp(), 10), nonce: "short", signature: strings.Repeat("a", 64), want: "malformed_nonce"},
		{name: "non hex signature", timestamp: strconv.FormatInt(common.GetTimestamp(), 10), nonce: freshNonce(), signature: "zz", want: "malformed_signature"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/signed", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+raw)
			request.Header.Set(RequestTimestampHeader, tc.timestamp)
			request.Header.Set(RequestNonceHeader, tc.nonce)
			request.Header.Set(RequestSignatureHeader, tc.signature)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), tc.want)
		})
	}
}

func TestRequestSignatureKeyIsTokenScoped(t *testing.T) {
	// 不同 token 派生不同密钥；同一 token 的签名可被服务端复算。
	first := RequestSignatureKey("token-a")
	second := RequestSignatureKey("token-b")
	assert.NotEqual(t, string(first), string(second))
	assert.Equal(t, string(RequestSignatureKey("token-a")), string(first))

	body := []byte(`{"amount":7}`)
	signature := SignRequestSignature("token-a", http.MethodPost, "/api/signed", body, 1700000000, strings.Repeat("n", 32))
	assert.Len(t, signature, 64)
	assert.NotEqual(t, signature, SignRequestSignature("token-b", http.MethodPost, "/api/signed", body, 1700000000, strings.Repeat("n", 32)))

	// 规范化串对方法大小写不敏感（HTTP/2 客户端可能发小写）。
	assert.Equal(t,
		RequestSignatureCanonicalString(http.MethodPost, "/api/signed", RequestSignatureBodyHash(body), 1700000000, strings.Repeat("n", 32)),
		RequestSignatureCanonicalString("post", "/api/signed", RequestSignatureBodyHash(body), 1700000000, strings.Repeat("n", 32)),
	)
}
