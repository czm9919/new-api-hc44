package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 大转盘 + QQ 绑定（v1.1.0 M2）路由级回归：
//   - 匿名不可访问面板接口
//   - 未绑定 QQ 抽奖被 403 拒绝（QQ_BINDING_REQUIRED）
//   - 机器人令牌未配置时绑定码接口 503、令牌错误时 401
//   - 绑定成功后抽奖成功，第二次受每日次数限制 429
func TestLotteryQQBindingFlow(t *testing.T) {
	engine := newLotteryTestEngine(t)
	user := createLotteryRouterUser(t, "lottery-user")

	// 1) 匿名 401
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/user/lottery", nil))
	assert.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())

	// 2) 登录用户查看状态：未绑定 QQ、默认 1 次
	body := doJSON(t, engine, http.MethodGet, "/api/user/lottery", user.accessToken, nil)
	require.Equal(t, http.StatusOK, body.status, body.raw)
	data := body.dataMap()
	assert.False(t, data["qq_bound"].(bool), "新用户默认未绑定 QQ")
	assert.Equal(t, float64(1), data["spins_remaining"])

	// 3) 未绑定抽奖 → 403 QQ_BINDING_REQUIRED
	spin := doJSON(t, engine, http.MethodPost, "/api/user/lottery/spin", user.accessToken, nil)
	require.Equal(t, http.StatusForbidden, spin.status, spin.raw)
	assert.Equal(t, "QQ_BINDING_REQUIRED", spin.codeMap()["code"])

	// 4) 机器人令牌未配置 → 绑定码接口 503
	codeReq := doJSON(t, engine, http.MethodPost, "/api/user/qq/bind/code", user.accessToken, nil)
	require.Equal(t, http.StatusServiceUnavailable, codeReq.status, codeReq.raw)

	// 5) 配置令牌后可以生成绑定码
	require.NoError(t, model.UpdateOption(service.QQAgentTokenOptionKey, "agent-secret-token"))
	codeReq = doJSON(t, engine, http.MethodPost, "/api/user/qq/bind/code", user.accessToken, nil)
	require.Equal(t, http.StatusOK, codeReq.status, codeReq.raw)
	bindCode, _ := codeReq.dataMap()["code"].(string)
	require.NotEmpty(t, bindCode)

	// 6) 机器人令牌错误 → 401
	wrong := doJSON(t, engine, http.MethodPost, "/api/qq/agent/verify", "", map[string]any{
		"code": bindCode, "qq": "123456789", "token": "wrong-token",
	})
	require.Equal(t, http.StatusUnauthorized, wrong.status, wrong.raw)

	// 7) 正确令牌 → 绑定成功
	ok := doJSON(t, engine, http.MethodPost, "/api/qq/agent/verify", "", map[string]any{
		"code": bindCode, "qq": "123456789", "token": "agent-secret-token",
	})
	require.Equal(t, http.StatusOK, ok.status, ok.raw)
	assert.Equal(t, float64(user.userID), ok.dataMap()["user_id"])

	// 8) 状态变为已绑定
	statusBody := doJSON(t, engine, http.MethodGet, "/api/user/lottery", user.accessToken, nil)
	require.Equal(t, http.StatusOK, statusBody.status, statusBody.raw)
	assert.True(t, statusBody.dataMap()["qq_bound"].(bool))

	// 9) 抽奖成功且次数用尽
	spin = doJSON(t, engine, http.MethodPost, "/api/user/lottery/spin", user.accessToken, nil)
	require.Equal(t, http.StatusOK, spin.status, spin.raw)
	spinData := spin.dataMap()
	assert.Equal(t, float64(0), spinData["spins_remaining"])
	assert.NotEmpty(t, spinData["prize"].(map[string]any)["name"])

	// 10) 第二次 → 429
	again := doJSON(t, engine, http.MethodPost, "/api/user/lottery/spin", user.accessToken, nil)
	require.Equal(t, http.StatusTooManyRequests, again.status, again.raw)
	assert.Equal(t, "LOTTERY_NO_SPINS", again.codeMap()["code"])

	// 11) 记录接口能看到刚才那一次
	records := doJSON(t, engine, http.MethodGet, "/api/user/lottery/records", user.accessToken, nil)
	require.Equal(t, http.StatusOK, records.status, records.raw)
	items, _ := records.dataMap()["items"].([]any)
	assert.Len(t, items, 1)
}

// 绑定码只能被一个 QQ 用掉；重放同一个码返回 QQ_BIND_CODE_INVALID。
func TestQQBindCodeCannotBeReplayed(t *testing.T) {
	engine := newLotteryTestEngine(t)
	first := createLotteryRouterUser(t, "replay-first")
	second := createLotteryRouterUser(t, "replay-second")
	require.NoError(t, model.UpdateOption(service.QQAgentTokenOptionKey, "agent-secret-token"))

	codeReq := doJSON(t, engine, http.MethodPost, "/api/user/qq/bind/code", first.accessToken, nil)
	require.Equal(t, http.StatusOK, codeReq.status, codeReq.raw)
	bindCode, _ := codeReq.dataMap()["code"].(string)
	require.NotEmpty(t, bindCode)

	ok := doJSON(t, engine, http.MethodPost, "/api/qq/agent/verify", "", map[string]any{
		"code": bindCode, "qq": "11112222", "token": "agent-secret-token",
	})
	require.Equal(t, http.StatusOK, ok.status, ok.raw)

	// 同一个码再给第二个账号用 → 失效
	replay := doJSON(t, engine, http.MethodPost, "/api/qq/agent/verify", "", map[string]any{
		"code": bindCode, "qq": "33334444", "token": "agent-secret-token",
	})
	require.Equal(t, http.StatusBadRequest, replay.status, replay.raw)
	assert.Equal(t, "QQ_BIND_CODE_INVALID", replay.codeMap()["code"])

	qq, err := model.GetUserQQ(second.userID)
	require.NoError(t, err)
	assert.Empty(t, qq)
}

// 管理端配置接口：普通用户 403、管理员可读写。
func TestAdminLotterySettingsRoute(t *testing.T) {
	engine := newLotteryTestEngine(t)
	commonUser := createLotteryRouterUser(t, "settings-user")
	admin := createLotteryRouterUser(t, "settings-admin")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", admin.userID).
		Update("role", common.RoleAdminUser).Error)
	// 让 admin 的会话带上新角色：重新签发一次
	adminBundle, err := service.CreateLoginSession(admin.userID, "password", "127.0.0.1", "lottery-settings-test")
	require.NoError(t, err)
	admin.accessToken = adminBundle.AccessToken

	forbidden := doJSON(t, engine, http.MethodGet, "/api/user/lottery/settings", commonUser.accessToken, nil)
	require.Equal(t, http.StatusForbidden, forbidden.status, forbidden.raw)

	read := doJSON(t, engine, http.MethodGet, "/api/user/lottery/settings", admin.accessToken, nil)
	require.Equal(t, http.StatusOK, read.status, read.raw)
	assert.Equal(t, float64(1), read.dataMap()["daily_spins"])

	write := doJSON(t, engine, http.MethodPut, "/api/user/lottery/settings", admin.accessToken, map[string]any{
		"enabled":     true,
		"daily_spins": 3,
		"require_qq":  false,
		"prizes": []map[string]any{
			{"id": "p1", "name": "P1", "type": "quota", "quota": 800, "weight": 1},
		},
	})
	require.Equal(t, http.StatusOK, write.status, write.raw)
	assert.Equal(t, float64(3), write.dataMap()["daily_spins"])
	assert.False(t, write.dataMap()["require_qq"].(bool))
}

type lotteryRouterUser struct {
	userID      int
	accessToken string
}

func newLotteryTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret
	previousMap := common.OptionMap

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{},
		&model.AuditLog{}, &model.Token{}, &model.Log{},
		&model.LotterySpin{}, &model.QQBindCode{},
		&model.SystemTask{}, &model.SystemTaskLock{},
	))
	model.DB = db
	model.LOG_DB = db
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "lottery-router-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
		common.OptionMap = previousMap
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	return engine
}

func createLotteryRouterUser(t *testing.T, username string) *lotteryRouterUser {
	t.Helper()
	user := &model.User{
		Username: username, Password: "password-placeholder", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		AffCode: "aff-" + username,
	}
	require.NoError(t, model.DB.Create(user).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "lottery-router-test")
	require.NoError(t, err)
	return &lotteryRouterUser{userID: user.Id, accessToken: bundle.AccessToken}
}

type apiResponse struct {
	status int
	raw    string
	parsed map[string]any
}

func (r *apiResponse) dataMap() map[string]any {
	data, _ := r.parsed["data"].(map[string]any)
	if data == nil {
		return map[string]any{}
	}
	return data
}

func (r *apiResponse) codeMap() map[string]any {
	if r.parsed == nil {
		return map[string]any{}
	}
	return r.parsed
}

func doJSON(t *testing.T, engine *gin.Engine, method, path, token string, payload any) *apiResponse {
	t.Helper()
	var bodyReader *bytes.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(encoded)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, bodyReader)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	// 关闭 CSRF 来源校验依赖的浏览器头，模拟服务端/手机端调用
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	response := &apiResponse{status: recorder.Code, raw: recorder.Body.String()}
	_ = json.Unmarshal(recorder.Body.Bytes(), &response.parsed)
	return response
}

// createLotteryRouterAdmin 建一个管理员并签发会话（AdminAuth 依赖 role）。
func createLotteryRouterAdmin(t *testing.T, username string) *lotteryRouterUser {
	t.Helper()
	user := createLotteryRouterUser(t, username)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.userID).
		Update("role", common.RoleAdminUser).Error)
	bundle, err := service.CreateLoginSession(user.userID, "password", "127.0.0.1", "lottery-router-test")
	require.NoError(t, err)
	user.accessToken = bundle.AccessToken
	return user
}

// 管理端保存：坏配置逐字段报错且不落库；恢复默认能一键回到默认奖池。
func TestAdminLotterySettingsValidationAndReset(t *testing.T) {
	engine := newLotteryTestEngine(t)
	admin := createLotteryRouterAdmin(t, "settings-admin-v2")

	bad := doJSON(t, engine, http.MethodPut, "/api/user/lottery/settings", admin.accessToken,
		map[string]any{
			"enabled":     true,
			"daily_spins": 999,
			"require_qq":  true,
			"prizes": []map[string]any{
				{"id": "", "name": "", "type": "cash", "quota": -1, "weight": 0},
			},
		})
	require.Equal(t, http.StatusBadRequest, bad.status, bad.raw)
	assert.Equal(t, "LOTTERY_SETTINGS_INVALID", bad.codeMap()["code"])
	fields, _ := bad.parsed["fields"].([]any)
	require.NotEmpty(t, fields, "必须返回字段级错误清单")
	seen := map[string]bool{}
	for _, item := range fields {
		if entry, ok := item.(map[string]any); ok {
			seen[entry["field"].(string)] = true
		}
	}
	for _, want := range []string{"daily_spins", "prizes[0].name", "prizes[0].type", "prizes[0].quota", "prizes[0].weight"} {
		assert.True(t, seen[want], "缺少字段错误 %s，实际 %v", want, seen)
	}

	// 校验失败不能改到线上配置
	read := doJSON(t, engine, http.MethodGet, "/api/user/lottery/settings", admin.accessToken, nil)
	require.Equal(t, http.StatusOK, read.status, read.raw)
	assert.Equal(t, float64(1), read.dataMap()["daily_spins"])

	// 先改成非默认值，再恢复默认
	write := doJSON(t, engine, http.MethodPut, "/api/user/lottery/settings", admin.accessToken,
		map[string]any{
			"enabled": false, "daily_spins": 9, "require_qq": false,
			"prizes": []map[string]any{
				{"id": "only", "name": "唯一奖", "type": "quota", "quota": 100, "weight": 1},
			},
		})
	require.Equal(t, http.StatusOK, write.status, write.raw)
	assert.Equal(t, float64(9), write.dataMap()["daily_spins"])

	reset := doJSON(t, engine, http.MethodPost, "/api/user/lottery/settings/reset", admin.accessToken, nil)
	require.Equal(t, http.StatusOK, reset.status, reset.raw)
	assert.Equal(t, float64(1), reset.dataMap()["daily_spins"])
	assert.True(t, reset.dataMap()["enabled"].(bool))
	prizes, _ := reset.dataMap()["prizes"].([]any)
	assert.Len(t, prizes, len(service.DefaultLotteryPrizes()))
}

// 机器人令牌生命周期：短令牌被拒 -> 轮换出可用令牌 -> 撤销后绑定码 503。
func TestAdminQQAgentTokenLifecycle(t *testing.T) {
	engine := newLotteryTestEngine(t)
	admin := createLotteryRouterAdmin(t, "qq-admin")
	user := createLotteryRouterUser(t, "qq-token-user")

	read := doJSON(t, engine, http.MethodGet, "/api/user/lottery/qq/settings", admin.accessToken, nil)
	require.Equal(t, http.StatusOK, read.status, read.raw)
	assert.False(t, read.dataMap()["agent_configured"].(bool))
	assert.Equal(t, "", read.dataMap()["agent_token_hint"])
	assert.Contains(t, read.dataMap(), "bound_users")
	assert.Contains(t, read.dataMap(), "active_bind_codes")

	short := doJSON(t, engine, http.MethodPut, "/api/user/lottery/qq/settings", admin.accessToken,
		map[string]any{"agent_token": "tooshort", "require_for_free": true})
	require.Equal(t, http.StatusBadRequest, short.status, short.raw)
	assert.Equal(t, "QQ_AGENT_TOKEN_INVALID", short.codeMap()["code"])
	fields, _ := short.parsed["fields"].([]any)
	require.Len(t, fields, 1)
	assert.Equal(t, "agent_token", fields[0].(map[string]any)["field"])

	rotate := doJSON(t, engine, http.MethodPost, "/api/user/lottery/qq/settings/rotate", admin.accessToken, nil)
	require.Equal(t, http.StatusOK, rotate.status, rotate.raw)
	token, _ := rotate.dataMap()["agent_token"].(string)
	require.Len(t, token, service.QQAgentTokenRandomBytes*2)
	config, _ := rotate.dataMap()["config"].(map[string]any)
	require.NotNil(t, config)
	assert.True(t, config["agent_configured"].(bool))
	assert.Equal(t, service.MaskQQAgentToken(token), config["agent_token_hint"])
	// 配置视图里绝不能回显明文令牌
	assert.NotContains(t, config, "agent_token")

	// 新令牌立刻可用（管理端看不到明文以外的东西，这里用刚拿到的明文直接打机器人接口）
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/qq/agent/ping", nil)
	request.Header.Set("X-QQ-Agent-Token", token)
	engine.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// 撤销后绑定码回到 503
	clear := doJSON(t, engine, http.MethodDelete, "/api/user/lottery/qq/settings", admin.accessToken, nil)
	require.Equal(t, http.StatusOK, clear.status, clear.raw)
	assert.False(t, clear.dataMap()["agent_configured"].(bool))

	codeReq := doJSON(t, engine, http.MethodPost, "/api/user/qq/bind/code", user.accessToken, nil)
	assert.Equal(t, http.StatusServiceUnavailable, codeReq.status, codeReq.raw)
}
