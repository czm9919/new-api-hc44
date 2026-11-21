package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/glebarez/sqlite"
)

func setupQQBindTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &QQBindCode{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
}

func createQQTestUser(t *testing.T, username string) *User {
	t.Helper()
	user := &User{
		Username: username,
		Password: "hashed-password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		// aff_code 有唯一索引，测试里显式给不同的值，避免两个用户撞空串
		AffCode: "aff-" + username,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func TestNormalizeQQ(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"normal", " 123456789 ", "123456789", false},
		{"shortest", "12345", "12345", false},
		{"empty", "  ", "", true},
		{"letters", "12345abc", "", true},
		{"too short", "1234", "", true},
		{"too long", "1234567890123", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeQQ(tc.raw)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBindQQByCodeHappyPath(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "qq-owner")

	code, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	require.Len(t, code.Code, QQBindCodeLength)

	result, err := BindQQByCode(code.Code, "123456789")
	require.NoError(t, err)
	assert.Equal(t, user.Id, result.UserId)
	assert.Equal(t, "123456789", result.QQ)

	qq, err := GetUserQQ(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "123456789", qq)

	bound, err := IsQQBound(user.Id)
	require.NoError(t, err)
	assert.True(t, bound)
}

// 绑定码一次性：同一个码换一个 QQ 再绑必须失败。
func TestBindQQByCodeIsSingleUse(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "qq-owner")
	other := createQQTestUser(t, "qq-other")

	code, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code.Code, "123456789")
	require.NoError(t, err)

	_, err = BindQQByCode(code.Code, "987654321")
	assert.ErrorIs(t, err, ErrQQBindCodeInvalid, "已使用的码不能再绑定")

	qq, err := GetUserQQ(other.Id)
	require.NoError(t, err)
	assert.Empty(t, qq, "第二个账号不应被写入 QQ")
}

func TestBindQQByCodeExpired(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "qq-owner")

	code, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	// 手工把有效期推到过去
	require.NoError(t, DB.Model(&QQBindCode{}).Where("id = ?", code.Id).
		Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)

	_, err = BindQQByCode(code.Code, "123456789")
	assert.ErrorIs(t, err, ErrQQBindCodeInvalid)
}

// 一个 QQ 只能绑一个账号。
func TestBindQQByCodeRejectsDuplicateQQ(t *testing.T) {
	setupQQBindTestDB(t)
	first := createQQTestUser(t, "first")
	second := createQQTestUser(t, "second")

	code1, err := CreateQQBindCode(first.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code1.Code, "55556666")
	require.NoError(t, err)

	code2, err := CreateQQBindCode(second.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code2.Code, "55556666")
	assert.ErrorIs(t, err, ErrQQAlreadyBound)

	qq, err := GetUserQQ(second.Id)
	require.NoError(t, err)
	assert.Empty(t, qq)
}

// 同一账号已绑 A，再用新码绑 B 应被拒（防洗号）。
func TestBindQQByCodeRejectsRebindToAnotherQQ(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "owner")

	code, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code.Code, "11112222")
	require.NoError(t, err)

	code2, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code2.Code, "33334444")
	assert.ErrorIs(t, err, ErrQQBindAccountAlreadyBound)

	qq, err := GetUserQQ(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "11112222", qq)
}

// 重复绑定同一个 QQ 幂等成功（机器人重试不报错）。
func TestBindQQByCodeIdempotentSameQQ(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "owner")

	code, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code.Code, "11112222")
	require.NoError(t, err)

	code2, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	result, err := BindQQByCode(code2.Code, "11112222")
	require.NoError(t, err)
	assert.Equal(t, "11112222", result.QQ)
}

// 新码会作废旧码：用户重新点「生成」后，之前复制出去的码失效。
func TestCreateQQBindCodeInvalidatesPrevious(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "owner")

	first, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	second, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	require.NotEqual(t, first.Code, second.Code)

	_, err = BindQQByCode(first.Code, "11112222")
	assert.ErrorIs(t, err, ErrQQBindCodeInvalid, "旧码应已被清理")

	_, err = BindQQByCode(second.Code, "11112222")
	require.NoError(t, err)
}

func TestUnbindQQ(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "owner")

	code, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code.Code, "11112222")
	require.NoError(t, err)

	require.NoError(t, UnbindQQ(user.Id))
	qq, err := GetUserQQ(user.Id)
	require.NoError(t, err)
	assert.Empty(t, qq)
}

// 老库升级后 users.qq 是 NULL（AutoMigrate 只给新行补默认值）。
// 这里锁住两件事：读路径不能因为 NULL 报错，启动回填要把 NULL 变成空串。
func TestGetUserQQToleratesLegacyNullColumn(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "legacy")
	require.NoError(t, DB.Exec("UPDATE users SET qq = NULL WHERE id = ?", user.Id).Error)

	qq, err := GetUserQQ(user.Id)
	require.NoError(t, err, "NULL 列不应让读路径报 Scan 错误")
	assert.Empty(t, qq)

	bound, err := IsQQBound(user.Id)
	require.NoError(t, err)
	assert.False(t, bound)

	require.NoError(t, EnsureUserQQBackfilled())

	var raw *string
	require.NoError(t, DB.Raw("SELECT qq FROM users WHERE id = ?", user.Id).Scan(&raw).Error)
	require.NotNil(t, raw)
	assert.Equal(t, "", *raw)
}

func TestEnsureUserQQBackfilledKeepsBoundValue(t *testing.T) {
	setupQQBindTestDB(t)
	user := createQQTestUser(t, "keepbound")
	code, err := CreateQQBindCode(user.Id)
	require.NoError(t, err)
	_, err = BindQQByCode(code.Code, "12345678")
	require.NoError(t, err)

	require.NoError(t, EnsureUserQQBackfilled())
	qq, err := GetUserQQ(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "12345678", qq)
}
