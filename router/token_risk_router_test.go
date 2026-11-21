package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// hc36 · B93：/api/token/risk/overview 是跨用户的管理端巡检接口。
// 回归护栏：它曾经挂在只做 UserAuth 的 /api/token 组里，任何登录用户
// 都能拉到全站密钥风险证据（用户名 + 来源 IP + 用量），属越权。
func TestTokenRiskOverviewRouteRequiresAdmin(t *testing.T) {
	engine := newTokenRiskTestEngine(t)
	commonUserToken := createTokenRiskTestUser(t, "risk-plain-user", common.RoleCommonUser)
	adminToken := createTokenRiskTestUser(t, "risk-admin-user", common.RoleAdminUser)
	seedTokenRiskFixture(t, commonUserToken.userID)

	gin.SetMode(gin.TestMode)
	for _, testCase := range []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "anonymous", token: "", wantStatus: http.StatusUnauthorized},
		{name: "common user", token: commonUserToken.accessToken, wantStatus: http.StatusForbidden},
		{name: "admin", token: adminToken.accessToken, wantStatus: http.StatusOK},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/token/risk/overview?days=30", nil)
			if testCase.token != "" {
				request.Header.Set("Authorization", "Bearer "+testCase.token)
			}
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, testCase.wantStatus, recorder.Code, recorder.Body.String())
		})
	}
}

// 管理员拿到的是全站视图：别的用户的 token 也在结果里，且带用户名。
func TestTokenRiskOverviewAdminSeesCrossUserEvidence(t *testing.T) {
	engine := newTokenRiskTestEngine(t)
	admin := createTokenRiskTestUser(t, "risk-admin-viewer", common.RoleAdminUser)
	owner := createTokenRiskTestUser(t, "risk-leaked-owner", common.RoleCommonUser)
	tokenID := seedTokenRiskFixture(t, owner.userID)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/token/risk/overview?days=30", nil)
	request.Header.Set("Authorization", "Bearer "+admin.accessToken)
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			WindowDays  int `json:"window_days"`
			TotalTokens int `json:"total_tokens"`
			Items       []struct {
				TokenID    int    `json:"token_id"`
				TokenName  string `json:"token_name"`
				Username   string `json:"username"`
				RiskLevel  string `json:"risk_level"`
				DistinctIP int    `json:"distinct_ips"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	assert.True(t, body.Success)
	require.Len(t, body.Data.Items, 1, recorder.Body.String())
	item := body.Data.Items[0]
	assert.Equal(t, tokenID, item.TokenID)
	assert.Equal(t, "risk-rotation-token", item.TokenName)
	assert.Equal(t, "risk-leaked-owner", item.Username)
	assert.Equal(t, service.TokenRiskLevelHigh, item.RiskLevel)
	assert.GreaterOrEqual(t, item.DistinctIP, service.TokenRiskHighIPThreshold)
}

type tokenRiskTestUser struct {
	userID      int
	accessToken string
}

func newTokenRiskTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	previousSecret := common.SessionSecret

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{},
		&model.AuditLog{}, &model.Token{}, &model.Log{},
	))
	model.DB = db
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "token-risk-router-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		common.SessionSecret = previousSecret
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	return engine
}

func createTokenRiskTestUser(t *testing.T, username string, role int) tokenRiskTestUser {
	t.Helper()
	user := &model.User{
		Username: username, Password: "password-placeholder", Role: role,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
		AffCode: "aff-" + username,
	}
	require.NoError(t, model.DB.Create(user).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "token-risk-router-test")
	require.NoError(t, err)
	return tokenRiskTestUser{userID: user.Id, accessToken: bundle.AccessToken}
}

// seedTokenRiskFixture 造一条「多 IP 使用」的证据：同一个 token 在
// HighIPThreshold 个以上不同 IP 上发起消费，足够被判为 high 风险。
func seedTokenRiskFixture(t *testing.T, userID int) int {
	t.Helper()
	token := &model.Token{
		UserId:      userID,
		Key:         fmt.Sprintf("sk-risk-%d", userID),
		Status:      common.TokenStatusEnabled,
		Name:        "risk-rotation-token",
		CreatedTime: time.Now().Unix(),
		ExpiredTime: -1,
	}
	require.NoError(t, model.DB.Create(token).Error)
	now := time.Now().Unix()
	for i := 0; i < service.TokenRiskHighIPThreshold; i++ {
		require.NoError(t, model.LOG_DB.Create(&model.Log{
			UserId:    userID,
			CreatedAt: now - int64(i),
			Type:      model.LogTypeConsume,
			TokenId:   token.Id,
			TokenName: token.Name,
			Ip:        fmt.Sprintf("203.0.113.%d", 10+i),
		}).Error)
	}
	return token.Id
}
