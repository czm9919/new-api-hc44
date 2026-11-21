package service

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/glebarez/sqlite"
)

// 大转盘抽奖（v1.1.0 M2）单测：奖池规范化、权重抽取、每日次数、QQ 门禁。

func setupLotteryTestDB(t *testing.T) {
	t.Helper()
	// 用临时文件而不是 :memory:：内存库按连接隔离，并发用例里每个
	// goroutine 拿到的是不同的库（会报 no such table）。文件库 + WAL +
	// busy_timeout 也更接近线上 SQLite 形态。
	dsn := filepath.Join(t.TempDir(), "lottery-test.db") +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.LotterySpin{}, &model.Option{}, &model.QQBindCode{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	previous := model.DB
	model.DB = db
	// UpdateOption 会写内存 OptionMap，测试里给个干净的 map（否则 nil map panic）
	previousMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	// Windows 上打开的文件句柄会挡住 TempDir 清理，先关连接再让 TempDir 删。
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	t.Cleanup(func() {
		model.DB = previous
		common.OptionMap = previousMap
	})
	common.BatchUpdateEnabled = false
}

func createLotteryTestUser(t *testing.T, username string) *model.User {
	t.Helper()
	user := &model.User{
		Username: username,
		Password: "hashed-password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		AffCode:  "aff-" + username,
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

func setLotteryOption(t *testing.T, key, value string) {
	t.Helper()
	require.NoError(t, model.UpdateOption(key, value))
}

func TestNormalizeLotteryPrizes(t *testing.T) {
	prizes := NormalizeLotteryPrizes([]LotteryPrize{
		{Id: "a", Name: "A", Type: LotteryPrizeTypeQuota, Quota: 100, Weight: 10},
		{Id: "", Name: "no id", Weight: 5},
		{Id: "b", Name: "", Weight: 5},
		{Id: "a", Name: "dup id", Weight: 5},
		{Id: "zero-weight", Name: "Z", Weight: 0},
		{Id: "c", Name: "C", Type: LotteryPrizeTypeQuota, Quota: 0, Weight: 3},
		{Id: "d", Name: "D", Type: "weird", Quota: 999, Weight: -1},
	})
	require.Len(t, prizes, 2)
	assert.Equal(t, "a", prizes[0].Id)
	assert.Equal(t, LotteryPrizeTypeQuota, prizes[0].Type)
	// 名义额度奖但额度为 0 → 归为未中奖，避免「中奖了但没给东西」
	assert.Equal(t, "c", prizes[1].Id)
	assert.Equal(t, LotteryPrizeTypeNone, prizes[1].Type)
	assert.Equal(t, 0, prizes[1].Quota)
}

func TestNormalizeLotteryPrizesCapsCount(t *testing.T) {
	var input []LotteryPrize
	for i := 0; i < LotteryMaxWheelPrizes+5; i++ {
		input = append(input, LotteryPrize{
			Id:     string(rune('a' + i)),
			Name:   "p",
			Type:   LotteryPrizeTypeQuota,
			Quota:  100,
			Weight: 1,
		})
	}
	assert.Len(t, NormalizeLotteryPrizes(input), LotteryMaxWheelPrizes)
}

func TestPickLotteryPrizeDistribution(t *testing.T) {
	prizes := []LotteryPrize{
		{Id: "none", Name: "谢谢参与", Type: LotteryPrizeTypeNone, Weight: 90},
		{Id: "big", Name: "大奖", Type: LotteryPrizeTypeQuota, Quota: 10000, Weight: 10},
	}
	rnd := rand.New(rand.NewSource(42))
	const rounds = 20000
	hits := map[string]int{}
	for i := 0; i < rounds; i++ {
		prize, err := PickLotteryPrize(prizes, rnd)
		require.NoError(t, err)
		hits[prize.Id]++
	}
	ratio := float64(hits["big"]) / float64(rounds)
	assert.InDelta(t, 0.10, ratio, 0.02, "10% 权重应抽出约 10%")
}

func TestPickLotteryPrizeEmptyPool(t *testing.T) {
	_, err := PickLotteryPrize(nil, nil)
	assert.Error(t, err)
	_, err = PickLotteryPrize([]LotteryPrize{{Id: "x", Name: "x", Weight: 0}}, nil)
	assert.Error(t, err)
}

// 未绑定 QQ 时抽奖被拒（默认配置要求 QQ）。
func TestSpinLotteryRequiresQQ(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "spin-user")

	_, err := SpinLottery(user.Id, false)
	assert.ErrorIs(t, err, ErrLotteryQQRequired)
}

func TestSpinLotteryGrantsFreeQuotaAndCountsDown(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "spin-user")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).
		Update("qq", "123456789").Error)
	setLotteryOption(t, LotteryRequireQQOptionKey, "false")
	// 只留一个必中的额度奖，便于断言发奖
	setLotteryOption(t, LotteryPrizesOptionKey,
		`[{"id":"q","name":"固定 1000","type":"quota","quota":1000,"weight":1}]`)
	setLotteryOption(t, LotteryDailySpinsOptionKey, "2")

	result, err := SpinLottery(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 1000, result.QuotaAwarded)
	assert.Equal(t, 1, result.SpinsUsed)
	assert.Equal(t, 1, result.SpinsRemaining)

	var after model.User
	require.NoError(t, model.DB.Where("id = ?", user.Id).Take(&after).Error)
	assert.Equal(t, 1000, after.Quota)
	assert.Equal(t, 1000, after.FreeQuota, "奖品走免费额度口径")

	// 第二次用完
	_, err = SpinLottery(user.Id, false)
	require.NoError(t, err)

	// 第三次超限
	_, err = SpinLottery(user.Id, false)
	assert.ErrorIs(t, err, ErrLotteryNoSpinsLeft)
}

func TestSpinLotteryDisabled(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "spin-user")
	setLotteryOption(t, LotteryEnabledOptionKey, "false")

	_, err := SpinLottery(user.Id, false)
	assert.ErrorIs(t, err, ErrLotteryDisabled)
}

func TestGetLotterySettingsDefaults(t *testing.T) {
	setupLotteryTestDB(t)
	settings := GetLotterySettings()
	assert.True(t, settings.Enabled)
	assert.True(t, settings.RequireQQ)
	assert.Equal(t, 1, settings.DailySpins)
	require.NotEmpty(t, settings.Prizes)

	total := 0
	for _, prize := range settings.Prizes {
		assert.Greater(t, prize.Weight, 0)
		total += prize.Weight
	}
	assert.Greater(t, total, 0)
}

func TestSaveAndReloadLotterySettings(t *testing.T) {
	setupLotteryTestDB(t)
	saved, err := SaveLotterySettings(LotterySettings{
		Enabled:    true,
		DailySpins: 3,
		RequireQQ:  false,
		Prizes: []LotteryPrize{
			{Id: "p1", Name: "P1", Type: LotteryPrizeTypeQuota, Quota: 700, Weight: 4},
		},
	})
	require.NoError(t, err)
	// 保存接口直接回读，前端拿到的是生效值
	assert.Equal(t, 3, saved.DailySpins)

	reloaded := GetLotterySettings()
	assert.Equal(t, 3, reloaded.DailySpins)
	assert.False(t, reloaded.RequireQQ)
	require.Len(t, reloaded.Prizes, 1)
	assert.Equal(t, 700, reloaded.Prizes[0].Quota)
}

func TestMaskQQ(t *testing.T) {
	assert.Equal(t, "123****89", MaskQQ("123456789"))
	assert.Equal(t, "*****", MaskQQ("12345"))
	assert.Equal(t, "", MaskQQ(""))
}

// 免费模型门禁：开关、免费模型白名单、免费级 key、已绑定放行。
func TestEnsureQQBoundForFreeUsage(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "free-user")

	model.SetFreeQuotaAllowedGroups("免费分组")
	model.SetFreeQuotaAllowedModels("gpt-3*,gemini-*")
	t.Cleanup(func() {
		model.SetFreeQuotaAllowedGroups("")
		model.SetFreeQuotaAllowedModels("")
	})

	// 白名单内模型 + 未绑定 → 拒绝
	err := EnsureQQBoundForFreeUsage(user.Id, nil, "免费分组", "gpt-3.5-turbo")
	assert.ErrorIs(t, err, ErrQQBindRequiredForFree)

	// 白名单外模型（付费模型）→ 不拦（即使分组是免费分组）
	assert.NoError(t, EnsureQQBoundForFreeUsage(user.Id, nil, "免费分组", "gpt-4o"))

	// 免费级 key（号池）即使在付费分组、付费模型也拦
	freeToken := &model.Token{SourceChannelId: 7}
	assert.ErrorIs(t, EnsureQQBoundForFreeUsage(user.Id, freeToken, "paid", "gpt-4o"), ErrQQBindRequiredForFree)

	// 绑定后放行
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).
		Update("qq", "123456789").Error)
	assert.NoError(t, EnsureQQBoundForFreeUsage(user.Id, nil, "免费分组", "gpt-3.5-turbo"))

	// 开关关闭 → 一律放行
	setLotteryOption(t, QQRequireForFreeOptionKey, "false")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("qq", "").Error)
	assert.NoError(t, EnsureQQBoundForFreeUsage(user.Id, freeToken, "免费分组", "gpt-3.5-turbo"))
}

// 免费模型白名单为空时不应误伤付费模型（这是 historically 最容易踩的坑）。
func TestEnsureQQBoundSkipsWhenFreeModelWhitelistEmpty(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "free-user-empty")
	model.SetFreeQuotaAllowedModels("")
	t.Cleanup(func() { model.SetFreeQuotaAllowedModels("") })

	assert.NoError(t, EnsureQQBoundForFreeUsage(user.Id, nil, "default", "gpt-4o"))
	assert.False(t, RequiresQQForFreeUsage(nil, "default", "gpt-4o"))
	assert.True(t, RequiresQQForFreeUsage(&model.Token{SourceChannelId: 1}, "default", "gpt-4o"),
		"免费级 key 与模型白名单无关，始终要绑定")
}

// 并发抽奖只有一个请求能拿到同一个每日额度（唯一索引兜底）。
func TestSpinLotteryConcurrentRespectsDailyLimit(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "race-user")
	setLotteryOption(t, LotteryRequireQQOptionKey, "false")
	setLotteryOption(t, LotteryDailySpinsOptionKey, "1")
	setLotteryOption(t, LotteryPrizesOptionKey,
		`[{"id":"q","name":"1","type":"quota","quota":1,"weight":1}]`)

	const workers = 8
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := SpinLottery(user.Id, false)
			results <- err
		}()
	}
	success := 0
	for i := 0; i < workers; i++ {
		if err := <-results; err == nil {
			success++
		}
	}
	assert.Equal(t, 1, success, "每日 1 次时并发 8 个请求只能成功 1 个")

	used, err := model.CountLotterySpinsOnDay(user.Id, model.LotteryDayKey(time.Now()))
	require.NoError(t, err)
	assert.Equal(t, int64(1), used)
}

// 管理端保存必须逐字段报错，而不是静默截断/丢行。
func TestValidateLotterySettingsRejectsBadInput(t *testing.T) {
	longName := strings.Repeat("奖", LotteryMaxPrizeNameRunes+1)
	manyPrizes := func(n int) []LotteryPrize {
		out := make([]LotteryPrize, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, LotteryPrize{
				Id: fmt.Sprintf("p%d", i), Name: "P", Type: LotteryPrizeTypeQuota, Quota: 100, Weight: 1,
			})
		}
		return out
	}
	cases := []struct {
		name      string
		mutate    func(*LotterySettings)
		wantField string
	}{
		{"每日次数超上限", func(s *LotterySettings) { s.DailySpins = LotteryMaxDailySpins + 1 }, "daily_spins"},
		{"每日次数为负", func(s *LotterySettings) { s.DailySpins = -1 }, "daily_spins"},
		{"奖池为空", func(s *LotterySettings) { s.Prizes = nil }, "prizes"},
		{"奖品过多", func(s *LotterySettings) { s.Prizes = manyPrizes(LotteryMaxWheelPrizes + 1) }, "prizes"},
		{"名称为空", func(s *LotterySettings) { s.Prizes[0].Name = "  " }, "prizes[0].name"},
		{"名称过长", func(s *LotterySettings) { s.Prizes[0].Name = longName }, "prizes[0].name"},
		{"类型非法", func(s *LotterySettings) { s.Prizes[0].Type = "cash" }, "prizes[0].type"},
		{"额度奖为 0", func(s *LotterySettings) {
			s.Prizes[0].Type = LotteryPrizeTypeQuota
			s.Prizes[0].Quota = 0
		}, "prizes[0].quota"},
		{"额度为负", func(s *LotterySettings) { s.Prizes[0].Quota = -5 }, "prizes[0].quota"},
		{"额度过大", func(s *LotterySettings) { s.Prizes[0].Quota = LotteryMaxPrizeQuota + 1 }, "prizes[0].quota"},
		{"权重为 0", func(s *LotterySettings) { s.Prizes[0].Weight = 0 }, "prizes[0].weight"},
		{"权重过大", func(s *LotterySettings) { s.Prizes[0].Weight = LotteryMaxPrizeWeight + 1 }, "prizes[0].weight"},
		{"标识重复", func(s *LotterySettings) { s.Prizes[1].Id = s.Prizes[0].Id }, "prizes[1].id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := LotterySettings{
				Enabled: true, DailySpins: 2, RequireQQ: true,
				Prizes: DefaultLotteryPrizes(),
			}
			tc.mutate(&settings)
			err := ValidateLotterySettings(settings)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrLotterySettingsInvalid)
			var invalid *LotterySettingsError
			require.ErrorAs(t, err, &invalid)
			fields := make([]string, 0, len(invalid.Fields))
			for _, f := range invalid.Fields {
				fields = append(fields, f.Field)
			}
			assert.Contains(t, fields, tc.wantField, "错误字段清单：%v", fields)
		})
	}
}

func TestValidateLotterySettingsAcceptsDefaults(t *testing.T) {
	assert.NoError(t, ValidateLotterySettings(LotterySettings{
		Enabled: true, DailySpins: LotteryMaxDailySpins, RequireQQ: false,
		Prizes: DefaultLotteryPrizes(),
	}))
	// 全 thank-you 池（无额度奖）也应合法：运营可能想先只发谢谢参与
	assert.NoError(t, ValidateLotterySettings(LotterySettings{
		Enabled: true, DailySpins: 1,
		Prizes: []LotteryPrize{{Id: "thanks", Name: "谢谢参与", Type: LotteryPrizeTypeNone, Weight: 10}},
	}))
}

func TestCanonicalizeLotteryPrizesDropsBlankRowAndFillsId(t *testing.T) {
	out := CanonicalizeLotteryPrizes([]LotteryPrize{
		{Name: "未填 id", Type: "none", Weight: 3},
		{}, // 整行空白 → 丢弃
		{Id: "q", Name: " 额度 ", Type: "QUOTA", Quota: 500, Weight: 1},
	})
	require.Len(t, out, 2)
	assert.Equal(t, "prize-1", out[0].Id)
	// 全空行被丢掉后，第二行仍是原始下标 2 → id 用 prize-3，保持可定位
	assert.Equal(t, "q", out[1].Id)
	assert.Equal(t, "额度", out[1].Name)
	assert.Equal(t, LotteryPrizeTypeQuota, out[1].Type)
	assert.Equal(t, 0, out[0].Quota, "none 类型不带额度")
}

func TestSaveLotterySettingsRejectsInvalidWithoutWriting(t *testing.T) {
	setupLotteryTestDB(t)
	_, err := SaveLotterySettings(LotterySettings{
		Enabled: true, DailySpins: 1,
		Prizes: []LotteryPrize{{Id: "bad", Name: "", Type: LotteryPrizeTypeQuota, Quota: 0, Weight: 0}},
	})
	require.Error(t, err)
	// 没写库：读回来仍是默认奖池
	assert.Equal(t, DefaultLotteryPrizes(), GetLotterySettings().Prizes)
}

func TestResetLotterySettings(t *testing.T) {
	setupLotteryTestDB(t)
	_, err := SaveLotterySettings(LotterySettings{
		Enabled: false, DailySpins: 7, RequireQQ: false,
		Prizes: []LotteryPrize{{Id: "only", Name: "只有一档", Type: LotteryPrizeTypeQuota, Quota: 100, Weight: 1}},
	})
	require.NoError(t, err)

	reset, err := ResetLotterySettings()
	require.NoError(t, err)
	assert.True(t, reset.Enabled)
	assert.Equal(t, 1, reset.DailySpins)
	assert.True(t, reset.RequireQQ)
	assert.Equal(t, DefaultLotteryPrizes(), reset.Prizes)
	// 付费抽奖默认关闭：运营显式打开才会向用户收费。
	assert.False(t, reset.PaidEnabled)
}

// ---------- 付费抽奖 ----------

// enablePaidLottery 打开付费抽奖，并把奖池换成「必中谢谢参与」，
// 这样额度变动只来自抽奖扣费，断言不被奖品干扰。
func enablePaidLottery(t *testing.T, price, limit int) {
	t.Helper()
	setLotteryOption(t, LotteryRequireQQOptionKey, "false")
	setLotteryOption(t, LotteryPaidEnabledOptionKey, "true")
	setLotteryOption(t, LotteryPaidPriceOptionKey, strconv.Itoa(price))
	setLotteryOption(t, LotteryPaidDailyLimitOptionKey, strconv.Itoa(limit))
	setLotteryOption(t, LotteryPrizesOptionKey,
		`[{"id":"none","name":"谢谢参与","type":"none","weight":1}]`)
}

func setLotteryTestWallet(t *testing.T, userId, quota, freeQuota int) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userId).
		Updates(map[string]any{"quota": quota, "free_quota": freeQuota}).Error)
}

func readLotteryTestWallet(t *testing.T, userId int) (int, int) {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Where("id = ?", userId).Take(&user).Error)
	return user.Quota, user.FreeQuota
}

// 默认配置下付费抽奖必须关闭（不能一上线就悄悄扣用户钱）。
func TestSpinLotteryPaidDisabledByDefault(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "paid-user")
	setLotteryOption(t, LotteryRequireQQOptionKey, "false")

	_, err := SpinLottery(user.Id, true)
	assert.ErrorIs(t, err, ErrLotteryPaidDisabled)
}

// 付费抽奖只扣付费余额，不动免费额度。
func TestSpinLotteryPaidDeductsPaidBalanceOnly(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "paid-user")
	enablePaidLottery(t, 1000, 3)
	setLotteryTestWallet(t, user.Id, 5000, 2000) // 付费余额 = 3000

	result, err := SpinLottery(user.Id, true)
	require.NoError(t, err)
	assert.True(t, result.Paid)
	assert.Equal(t, 1000, result.QuotaCost)
	assert.Equal(t, 2, result.PaidSpinsLeft)
	assert.Equal(t, 1, result.PaidSpinsUsed)

	quota, freeQuota := readLotteryTestWallet(t, user.Id)
	assert.Equal(t, 4000, quota, "工作余额扣掉一次价格")
	assert.Equal(t, 2000, freeQuota, "免费额度不能被抽奖吃掉")

	var spin model.LotterySpin
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).Take(&spin).Error)
	assert.True(t, spin.Paid)
	assert.Equal(t, 1000, spin.QuotaCost)
}

// 付费余额不足：拒绝，且一分钱都不能扣、记录也不能落。
func TestSpinLotteryPaidInsufficientBalance(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "paid-poor")
	enablePaidLottery(t, 1000, 3)
	setLotteryTestWallet(t, user.Id, 2000, 2000) // 付费余额 = 0

	_, err := SpinLottery(user.Id, true)
	assert.ErrorIs(t, err, ErrLotteryInsufficientQuota)

	quota, freeQuota := readLotteryTestWallet(t, user.Id)
	assert.Equal(t, 2000, quota)
	assert.Equal(t, 2000, freeQuota)
	used, err := model.CountLotterySpinsOnDay(user.Id, model.LotteryDayKey(time.Now()))
	require.NoError(t, err)
	assert.Equal(t, int64(0), used, "扣费失败不应留下抽奖记录")
}

// 付费次数与免费次数分开计：免费抽完还能付费抽，互不占用。
func TestSpinLotteryPaidDoesNotConsumeFreeSpins(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "paid-mixed")
	enablePaidLottery(t, 500, 2)
	setLotteryOption(t, LotteryDailySpinsOptionKey, "1")
	setLotteryTestWallet(t, user.Id, 10000, 0)

	_, err := SpinLottery(user.Id, false)
	require.NoError(t, err)
	_, err = SpinLottery(user.Id, false)
	assert.ErrorIs(t, err, ErrLotteryNoSpinsLeft, "免费次数已用完")

	// 付费通道不受免费次数限制
	_, err = SpinLottery(user.Id, true)
	require.NoError(t, err)

	status, err := GetLotteryStatus(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, status.SpinsRemaining, "免费次数仍为 0")
	assert.Equal(t, 1, status.PaidSpinsUsed)
	assert.Equal(t, 1, status.PaidSpinsRemaining)
	assert.Equal(t, 9500, status.Quota)
	assert.Equal(t, 9500, status.PaidQuota)
	assert.True(t, status.PaidAffordable)
}

// 付费每日上限生效。
func TestSpinLotteryPaidDailyLimit(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "paid-limit")
	enablePaidLottery(t, 100, 2)
	setLotteryTestWallet(t, user.Id, 100000, 0)

	for i := 0; i < 2; i++ {
		_, err := SpinLottery(user.Id, true)
		require.NoError(t, err)
	}
	_, err := SpinLottery(user.Id, true)
	assert.ErrorIs(t, err, ErrLotteryPaidNoSpinsLeft)

	quota, _ := readLotteryTestWallet(t, user.Id)
	assert.Equal(t, 99800, quota, "被拒的那次不能扣费")
}

// 并发付费抽奖：只能成功上限次数，且余额只被扣成功次数（失败必须退款）。
func TestSpinLotteryPaidConcurrentChargesOnlySuccesses(t *testing.T) {
	setupLotteryTestDB(t)
	user := createLotteryTestUser(t, "paid-race")
	enablePaidLottery(t, 1000, 1)
	setLotteryTestWallet(t, user.Id, 10000, 0)

	const workers = 6
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := SpinLottery(user.Id, true)
			results <- err
		}()
	}
	success := 0
	for i := 0; i < workers; i++ {
		if err := <-results; err == nil {
			success++
		}
	}
	assert.Equal(t, 1, success, "付费上限 1 次时并发只能成功 1 个")

	quota, _ := readLotteryTestWallet(t, user.Id)
	assert.Equal(t, 10000-1000*success, quota, "只扣成功次数对应的额度，失败必须退款")

	paidUsed, err := model.CountLotteryPaidSpinsOnDay(user.Id, model.LotteryDayKey(time.Now()))
	require.NoError(t, err)
	assert.Equal(t, int64(1), paidUsed)
}

// 付费参数校验：开关打开时必须价格 > 0、次数 > 0，越界要逐字段报错。
func TestValidateLotterySettingsPaidFields(t *testing.T) {
	base := func() LotterySettings {
		return LotterySettings{
			Enabled: true, DailySpins: 1, RequireQQ: false,
			Prizes:         DefaultLotteryPrizes(),
			PaidEnabled:    true,
			PaidPrice:      5000,
			PaidDailyLimit: 5,
		}
	}
	require.NoError(t, ValidateLotterySettings(base()))

	cases := []struct {
		name      string
		mutate    func(*LotterySettings)
		wantField string
	}{
		{"价格超出上限", func(s *LotterySettings) { s.PaidPrice = LotteryMaxPaidPrice + 1 }, "paid_price"},
		{"价格为负", func(s *LotterySettings) { s.PaidPrice = -1 }, "paid_price"},
		{"开启但价格为零", func(s *LotterySettings) { s.PaidPrice = 0 }, "paid_price"},
		{"次数超出上限", func(s *LotterySettings) { s.PaidDailyLimit = LotteryMaxPaidDailyLimit + 1 }, "paid_daily_limit"},
		{"次数为负", func(s *LotterySettings) { s.PaidDailyLimit = -1 }, "paid_daily_limit"},
		{"开启但次数为零", func(s *LotterySettings) { s.PaidDailyLimit = 0 }, "paid_daily_limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := base()
			tc.mutate(&settings)
			err := ValidateLotterySettings(settings)
			require.Error(t, err)
			var invalid *LotterySettingsError
			require.ErrorAs(t, err, &invalid)
			fields := make([]string, 0, len(invalid.Fields))
			for _, f := range invalid.Fields {
				fields = append(fields, f.Field)
			}
			assert.Contains(t, fields, tc.wantField, "错误字段清单：%v", fields)
		})
	}
}

// 付费配置能存下去也能读回来（避免「保存成功但读回默认值」）。
func TestSaveAndReloadPaidLotterySettings(t *testing.T) {
	setupLotteryTestDB(t)
	saved, err := SaveLotterySettings(LotterySettings{
		Enabled: true, DailySpins: 2, RequireQQ: false,
		Prizes:         DefaultLotteryPrizes(),
		PaidEnabled:    true,
		PaidPrice:      8000,
		PaidDailyLimit: 9,
	})
	require.NoError(t, err)
	assert.True(t, saved.PaidEnabled)
	assert.Equal(t, 8000, saved.PaidPrice)
	assert.Equal(t, 9, saved.PaidDailyLimit)

	reloaded := GetLotterySettings()
	assert.True(t, reloaded.PaidEnabled)
	assert.Equal(t, 8000, reloaded.PaidPrice)
	assert.Equal(t, 9, reloaded.PaidDailyLimit)
}

