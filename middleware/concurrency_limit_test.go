package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetRelayConcurrencyGate(t *testing.T, enabled bool, userLimit, modelLimit int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	relayConcurrencyGateInstance = &relayConcurrencyGate{
		users:  make(map[int]int),
		models: make(map[string]int),
	}
	oldEnabled := setting.RelayConcurrencyLimitEnabled
	oldUser := setting.RelayUserConcurrencyLimit
	oldModel := setting.RelayModelConcurrencyLimit
	setting.RelayConcurrencyLimitEnabled = enabled
	setting.RelayUserConcurrencyLimit = userLimit
	setting.RelayModelConcurrencyLimit = modelLimit
	t.Cleanup(func() {
		setting.RelayConcurrencyLimitEnabled = oldEnabled
		setting.RelayUserConcurrencyLimit = oldUser
		setting.RelayModelConcurrencyLimit = oldModel
	})
}

func TestAcquireUserConcurrencyLimit(t *testing.T) {
	resetRelayConcurrencyGate(t, true, 1, 0)

	release, exceeded := relayConcurrencyGateInstance.acquire(7, "gpt-4", 1, 0)
	require.NotNil(t, release)
	assert.Empty(t, exceeded)

	// 同一用户第二个并发请求撞用户上限
	release2, exceeded2 := relayConcurrencyGateInstance.acquire(7, "gpt-4", 1, 0)
	assert.Nil(t, release2)
	assert.Equal(t, "user", exceeded2)

	// 其他用户不受影响
	release3, exceeded3 := relayConcurrencyGateInstance.acquire(8, "gpt-4", 1, 0)
	require.NotNil(t, release3)
	assert.Empty(t, exceeded3)
	release3()
	release()
}

func TestAcquireModelConcurrencyLimit(t *testing.T) {
	resetRelayConcurrencyGate(t, true, 0, 1)

	release, exceeded := relayConcurrencyGateInstance.acquire(7, "gpt-4", 0, 1)
	require.NotNil(t, release)
	assert.Empty(t, exceeded)

	release2, exceeded2 := relayConcurrencyGateInstance.acquire(8, "gpt-4", 0, 1)
	assert.Nil(t, release2)
	assert.Equal(t, "model", exceeded2)

	// 不同模型共享用户但不共享模型额度
	release3, exceeded3 := relayConcurrencyGateInstance.acquire(7, "claude-3", 0, 1)
	require.NotNil(t, release3)
	assert.Empty(t, exceeded3)

	// 空模型名（模型还没解析出来）不受模型维度限制
	release4, _ := relayConcurrencyGateInstance.acquire(9, "", 0, 1)
	require.NotNil(t, release4)

	release()
	release3()
	release4()
}

func TestAcquireZeroLimitUnlimited(t *testing.T) {
	resetRelayConcurrencyGate(t, true, 0, 0)

	// 双 0 表示都不限制：任意多占用都成功，且不产生任何计数键
	for i := 0; i < 5; i++ {
		release, _ := relayConcurrencyGateInstance.acquire(i, "gpt-4", 0, 0)
		require.NotNil(t, release)
		release()
	}
	users, models := relayConcurrencyGateInstance.inFlight()
	assert.Equal(t, 0, users)
	assert.Equal(t, 0, models)
}

func TestAcquireReleaseCleansUp(t *testing.T) {
	resetRelayConcurrencyGate(t, true, 2, 2)

	releases := make([]func(), 0, 2)
	for i := 0; i < 2; i++ {
		release, _ := relayConcurrencyGateInstance.acquire(42, "gpt-4", 2, 2)
		require.NotNil(t, release)
		releases = append(releases, release)
	}
	_, exceeded := relayConcurrencyGateInstance.acquire(42, "gpt-4", 2, 2)
	assert.NotNil(t, exceeded)

	for _, release := range releases {
		release()
	}
	users, models := relayConcurrencyGateInstance.inFlight()
	assert.Equal(t, 0, users, "释放后不应残留用户计数键")
	assert.Equal(t, 0, models, "释放后不应残留模型计数键")
}

// stubTokenAuth 模拟 TokenAuth：从 X-Test-User 头读出用户 id 写入上下文。
// ServeHTTP 会重建 gin.Context，无法用 c.Set 预置，只能走真正的中间件链。
func stubTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		userId, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", userId)
		c.Next()
	}
}

func newConcurrencyTestRouter(handler gin.HandlerFunc) *gin.Engine {
	router := gin.New()
	router.POST("/v1/chat/completions", stubTokenAuth(), RelayConcurrencyLimit(), handler)
	return router
}

func doConcurrencyRequest(router *gin.Engine, userId int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Test-User", strconv.Itoa(userId))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestRelayConcurrencyLimitDisabled(t *testing.T) {
	resetRelayConcurrencyGate(t, false, 1, 1)

	router := newConcurrencyTestRouter(func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	for i := 0; i < 3; i++ {
		w := doConcurrencyRequest(router, 1)
		assert.Equal(t, http.StatusOK, w.Code)
	}
}

func TestRelayConcurrencyLimitUserExceeded(t *testing.T) {
	resetRelayConcurrencyGate(t, true, 1, 0)

	gate := make(chan struct{})
	releaseGate := make(chan struct{})
	var once sync.Once
	router := newConcurrencyTestRouter(func(c *gin.Context) {
		once.Do(func() { close(gate) })
		<-releaseGate // 第一个请求卡住，占满用户并发额度
		c.String(http.StatusOK, "ok")
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doConcurrencyRequest(router, 1)
	}()

	select {
	case <-gate:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never reached the handler")
	}

	// 第二个请求立即 429，不排队
	w := doConcurrencyRequest(router, 1)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))

	// 放行第一个请求，释放额度后新请求恢复正常
	close(releaseGate)
	first := <-done
	assert.Equal(t, http.StatusOK, first.Code)

	w2 := doConcurrencyRequest(router, 1)
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestRelayConcurrencyLimitModelExceeded(t *testing.T) {
	resetRelayConcurrencyGate(t, true, 0, 1)

	gate := make(chan struct{})
	releaseGate := make(chan struct{})
	var once sync.Once
	router := gin.New()
	// 直接注入 original_model，模拟 Distribute 已解析出模型名
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(string(constant.ContextKeyOriginalModel), "gpt-4")
	}, stubTokenAuth(), RelayConcurrencyLimit(), func(c *gin.Context) {
		once.Do(func() { close(gate) })
		<-releaseGate
		c.String(http.StatusOK, "ok")
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doConcurrencyRequest(router, 1)
	}()
	select {
	case <-gate:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never reached the handler")
	}

	// 同模型第二个用户也撞模型上限
	w := doConcurrencyRequest(router, 2)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	close(releaseGate)
	first := <-done
	assert.Equal(t, http.StatusOK, first.Code)
}
