package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 大转盘抽奖（v1.1.0 M2）
//
// 三件事在这里收敛：
//  1. 奖池与开关从 option 读，运营可在面板改 JSON，不改代码
//  2. 每日次数用 (user_id, day_key, spin_index) 唯一索引兜底，并发安全
//  3. 奖品发放统一走 IncreaseUserFreeQuota（免费额度），与签到同口径：
//     既涨 free_quota 也涨 quota，免费模型可用

const (
	// LotteryEnabledOptionKey 抽奖总开关。
	LotteryEnabledOptionKey = "lottery_enabled"
	// LotteryDailySpinsOptionKey 每日免费抽奖次数。
	LotteryDailySpinsOptionKey = "lottery_daily_spins"
	// LotteryPrizesOptionKey 奖池 JSON。
	LotteryPrizesOptionKey = "lottery_prizes"
	// LotteryRequireQQOptionKey 抽奖是否要求绑定 QQ。
	LotteryRequireQQOptionKey = "lottery_require_qq"
	// LotteryPaidEnabledOptionKey 付费抽奖开关（免费次数用完后可用余额继续抽）。
	LotteryPaidEnabledOptionKey = "lottery_paid_enabled"
	// LotteryPaidPriceOptionKey 单次付费抽奖价格（额度）。
	LotteryPaidPriceOptionKey = "lottery_paid_price"
	// LotteryPaidDailyLimitOptionKey 每日付费抽奖次数上限。
	LotteryPaidDailyLimitOptionKey = "lottery_paid_daily_limit"

	// LotteryMaxDailySpins 每日次数上限（防止运营配出天文数字）。
	LotteryMaxDailySpins = 50
	// LotteryMaxPaidDailyLimit 每日付费次数上限（比免费宽松一档，仍要有天花板）。
	LotteryMaxPaidDailyLimit = 200
	// LotteryMaxPaidPrice 单次付费抽奖价格上限（防止手抖多打几个 0 把用户余额掏空）。
	LotteryMaxPaidPrice = 1000000
	// LotteryMaxWheelPrizes 奖池最多奖品数（前端转盘扇区可读性上限）。
	LotteryMaxWheelPrizes = 12

	// LotteryMaxPrizeQuota 单个奖品的额度上限（防止手抖多打几个 0）。
	LotteryMaxPrizeQuota = 1000000
	// LotteryMaxPrizeWeight 单个奖品的权重上限。
	LotteryMaxPrizeWeight = 1000
	// LotteryMinPrizeWeight 权重下限：0 权重的扇区对抽奖没有意义，读侧会被丢掉，
	// 所以保存时直接拦下来，避免「存进去却不显示」的困惑。
	LotteryMinPrizeWeight = 1
	// LotteryMaxPrizeNameRunes 奖品名称长度上限（默认奖池里最长的一条是
	// 「30000 额度（头奖）」，留出余量给运营写说明）。
	LotteryMaxPrizeNameRunes = 24

	// LotteryPrizeTypeQuota 额度奖励。
	LotteryPrizeTypeQuota = "quota"
	// LotteryPrizeTypeNone 未中奖（谢谢参与）。
	LotteryPrizeTypeNone = "none"
)

// LotteryPrize 一个转盘扇区。
type LotteryPrize struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Quota int    `json:"quota"`
	// QuotaUSD 奖品的美元报价。
	// 读侧填充、写侧接受，**不落库**：库里只存 quota，美元只是展示口径，
	// 换算随 QuotaPerUnit 走，存下来反而会在改配置后变成历史垃圾。
	QuotaUSD float64 `json:"quota_usd,omitempty"`
	Weight   int     `json:"weight"`
}

// LotterySettings 抽奖的运营配置。
type LotterySettings struct {
	Enabled    bool           `json:"enabled"`
	DailySpins int            `json:"daily_spins"`
	RequireQQ  bool           `json:"require_qq"`
	Prizes     []LotteryPrize `json:"prizes"`

	// PaidEnabled 付费抽奖总开关。默认关闭：运营显式打开才会向用户收费。
	PaidEnabled bool `json:"paid_enabled"`
	// PaidPrice 单次付费抽奖价格（额度，只扣付费余额，不动免费额度）。
	// 对外报价用 PaidPriceUSD，额度字段保留给计费链路。
	PaidPrice int `json:"paid_price"`
	// PaidPriceUSD 单次付费抽奖的美元报价（唯一对外口径）。
	PaidPriceUSD float64 `json:"paid_price_usd"`
	// PaidDailyLimit 每日付费抽奖次数上限，与免费次数分开计算。
	PaidDailyLimit int `json:"paid_daily_limit"`
}

// DefaultLotteryPrizes 默认奖池：权重合计 95，头奖约 2%。
func DefaultLotteryPrizes() []LotteryPrize {
	prizes := []LotteryPrize{
		{Id: "thanks", Name: "谢谢参与", Type: LotteryPrizeTypeNone, Quota: 0, Weight: 40},
		{Id: "quota-500", Name: "500 额度", Type: LotteryPrizeTypeQuota, Quota: 500, Weight: 25},
		{Id: "quota-2000", Name: "2000 额度", Type: LotteryPrizeTypeQuota, Quota: 2000, Weight: 15},
		{Id: "quota-5000", Name: "5000 额度", Type: LotteryPrizeTypeQuota, Quota: 5000, Weight: 8},
		{Id: "quota-10000", Name: "10000 额度", Type: LotteryPrizeTypeQuota, Quota: 10000, Weight: 5},
		{Id: "quota-30000", Name: "30000 额度（头奖）", Type: LotteryPrizeTypeQuota, Quota: 30000, Weight: 2},
	}
	// 默认奖池也要带美元报价：否则「恢复默认」后的响应里 quota_usd 全是 0，
	// 管理端显示成一排 $0，看起来像配置丢了。
	for i := range prizes {
		prizes[i].QuotaUSD = common.RoundUSD(common.QuotaToUSD(int64(prizes[i].Quota)))
	}
	return prizes
}

func defaultLotterySettings() LotterySettings {
	return LotterySettings{
		Enabled:        true,
		DailySpins:     1,
		RequireQQ:      true,
		Prizes:         DefaultLotteryPrizes(),
		PaidEnabled:    false,
		PaidPrice:      5000,
		PaidDailyLimit: 5,
	}
}

// NormalizeLotteryPrizes 校验并规范化奖池：丢掉非法项，限制数量。
func NormalizeLotteryPrizes(prizes []LotteryPrize) []LotteryPrize {
	out := make([]LotteryPrize, 0, len(prizes))
	seen := make(map[string]struct{}, len(prizes))
	for _, prize := range prizes {
		id := strings.TrimSpace(prize.Id)
		name := strings.TrimSpace(prize.Name)
		if id == "" || name == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		prizeType := strings.ToLower(strings.TrimSpace(prize.Type))
		if prizeType != LotteryPrizeTypeQuota {
			prizeType = LotteryPrizeTypeNone
		}
		quota := prize.Quota
		if prizeType == LotteryPrizeTypeNone {
			quota = 0
		}
		if quota < 0 {
			quota = 0
		}
		if prizeType == LotteryPrizeTypeQuota && quota == 0 {
			// 名义上是额度奖但额度为 0，等同于未中奖，避免展示歧义。
			prizeType = LotteryPrizeTypeNone
		}
		weight := prize.Weight
		if weight <= 0 {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, LotteryPrize{
			Id:       id,
			Name:     name,
			Type:     prizeType,
			Quota:    quota,
			QuotaUSD: common.RoundUSD(common.QuotaToUSD(int64(quota))),
			Weight:   weight,
		})
		if len(out) >= LotteryMaxWheelPrizes {
			break
		}
	}
	return out
}

// CanonicalizeLotteryPrizes 管理端保存前的轻规范：去空白、补 id、统一 type。
//
// 与 NormalizeLotteryPrizes 的分工：
//   - 这个函数**不丢任何有内容的行**，非法项留给 ValidateLotterySettings 逐条报错，
//     管理员才能看到「第几行哪里错了」；
//   - NormalizeLotteryPrizes 是读取侧的宽容解析，宁可丢行也不让脏数据进转盘。
func CanonicalizeLotteryPrizes(prizes []LotteryPrize) []LotteryPrize {
	out := make([]LotteryPrize, 0, len(prizes))
	for i, prize := range prizes {
		name := strings.TrimSpace(prize.Name)
		id := strings.TrimSpace(prize.Id)
		prizeType := strings.ToLower(strings.TrimSpace(prize.Type))
		// 整行空白（前端「添加一行」后没填就点保存）当作误操作丢掉。
		if id == "" && name == "" && prize.Quota == 0 && prize.Weight == 0 && prizeType == "" {
			continue
		}
		if id == "" {
			id = fmt.Sprintf("prize-%d", i+1)
		}
		if prizeType == "" {
			prizeType = LotteryPrizeTypeNone
		}
		quota := prize.Quota
		if prizeType == LotteryPrizeTypeNone {
			quota = 0
		}
		// 美元报价优先：管理端现在只填美元，额度由服务端换算；
		// 老客户端仍可只传 quota（此时 QuotaUSD 为 0，不覆盖）。
		if prizeType == LotteryPrizeTypeQuota && prize.QuotaUSD > 0 {
			quota = int(common.USDToQuota(prize.QuotaUSD))
		}
		out = append(out, LotteryPrize{
			Id:     id,
			Name:   name,
			Type:   prizeType,
			Quota:  quota,
			Weight: prize.Weight,
		})
	}
	return out
}

// LotterySettingsFieldError 一条字段级校验错误，前端按 field 定位到具体行/控件。
type LotterySettingsFieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ErrLotterySettingsInvalid 抽奖配置校验失败（errors.Is 用）。
var ErrLotterySettingsInvalid = errors.New("invalid lottery settings")

// LotterySettingsError 携带字段清单的校验错误。
type LotterySettingsError struct {
	Fields []LotterySettingsFieldError
}

func (e *LotterySettingsError) Error() string {
	if len(e.Fields) == 0 {
		return ErrLotterySettingsInvalid.Error()
	}
	parts := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		parts = append(parts, field.Field+": "+field.Message)
	}
	return ErrLotterySettingsInvalid.Error() + " (" + strings.Join(parts, "; ") + ")"
}

// Is 让 errors.Is(err, ErrLotterySettingsInvalid) 成立。
func (e *LotterySettingsError) Is(target error) bool {
	return target == ErrLotterySettingsInvalid
}

// ValidateLotterySettings 逐字段校验管理端提交的配置。
//
// 保存路径**不做静默修正**：越界/空名/重复 id 一律报错回到前端，
// 免得运营以为改了实际被截断。
func ValidateLotterySettings(settings LotterySettings) error {
	fields := make([]LotterySettingsFieldError, 0, 4)
	if settings.DailySpins < 0 || settings.DailySpins > LotteryMaxDailySpins {
		fields = append(fields, LotterySettingsFieldError{
			Field:   "daily_spins",
			Message: fmt.Sprintf("每日次数需在 0 到 %d 之间", LotteryMaxDailySpins),
		})
	}
	if settings.PaidPrice < 0 || settings.PaidPrice > LotteryMaxPaidPrice {
		fields = append(fields, LotterySettingsFieldError{
			Field:   "paid_price",
			Message: fmt.Sprintf("单次价格需在 0 到 %.2f 美元之间", common.QuotaToUSD(LotteryMaxPaidPrice)),
		})
	}
	if settings.PaidDailyLimit < 0 || settings.PaidDailyLimit > LotteryMaxPaidDailyLimit {
		fields = append(fields, LotterySettingsFieldError{
			Field:   "paid_daily_limit",
			Message: fmt.Sprintf("每日付费次数需在 0 到 %d 之间", LotteryMaxPaidDailyLimit),
		})
	}
	if settings.PaidEnabled {
		// 开关打开时价格必须为正、次数必须为正，否则用户点付费抽奖只会得到报错。
		if settings.PaidPrice <= 0 {
			fields = append(fields, LotterySettingsFieldError{
				Field:   "paid_price",
				Message: "开启付费抽奖时必须设置大于 0 的单次价格",
			})
		}
		if settings.PaidDailyLimit <= 0 {
			fields = append(fields, LotterySettingsFieldError{
				Field:   "paid_daily_limit",
				Message: "开启付费抽奖时每日付费次数必须大于 0",
			})
		}
	}
	if len(settings.Prizes) == 0 {
		fields = append(fields, LotterySettingsFieldError{
			Field:   "prizes",
			Message: "奖池至少要有 1 个奖品",
		})
	}
	if len(settings.Prizes) > LotteryMaxWheelPrizes {
		fields = append(fields, LotterySettingsFieldError{
			Field:   "prizes",
			Message: fmt.Sprintf("奖池最多 %d 个奖品（转盘扇区上限）", LotteryMaxWheelPrizes),
		})
	}

	seen := make(map[string]int, len(settings.Prizes))
	totalWeight := 0
	for i, prize := range settings.Prizes {
		prefix := fmt.Sprintf("prizes[%d]", i)
		name := strings.TrimSpace(prize.Name)
		switch {
		case name == "":
			fields = append(fields, LotterySettingsFieldError{prefix + ".name", "奖品名称不能为空"})
		case utf8.RuneCountInString(name) > LotteryMaxPrizeNameRunes:
			fields = append(fields, LotterySettingsFieldError{
				prefix + ".name",
				fmt.Sprintf("奖品名称最多 %d 个字", LotteryMaxPrizeNameRunes),
			})
		}
		if prize.Type != LotteryPrizeTypeQuota && prize.Type != LotteryPrizeTypeNone {
			fields = append(fields, LotterySettingsFieldError{
				prefix + ".type",
				"奖品类型只能是 quota（额度）或 none（谢谢参与）",
			})
		}
		if prize.Quota < 0 {
			fields = append(fields, LotterySettingsFieldError{prefix + ".quota", "奖品金额不能为负数"})
		} else if prize.Quota > LotteryMaxPrizeQuota {
			fields = append(fields, LotterySettingsFieldError{
				prefix + ".quota",
				fmt.Sprintf("单奖金额不能超过 %.2f 美元", common.QuotaToUSD(LotteryMaxPrizeQuota)),
			})
		} else if prize.Type == LotteryPrizeTypeQuota && prize.Quota == 0 {
			fields = append(fields, LotterySettingsFieldError{
				prefix + ".quota",
				"金额奖品必须填正数金额（不给奖励请选「谢谢参与」）",
			})
		}
		if prize.Weight < LotteryMinPrizeWeight || prize.Weight > LotteryMaxPrizeWeight {
			fields = append(fields, LotterySettingsFieldError{
				prefix + ".weight",
				fmt.Sprintf("权重需在 %d 到 %d 之间", LotteryMinPrizeWeight, LotteryMaxPrizeWeight),
			})
		}
		id := strings.TrimSpace(prize.Id)
		if id == "" {
			fields = append(fields, LotterySettingsFieldError{prefix + ".id", "奖品标识不能为空"})
		} else if prev, dup := seen[id]; dup {
			fields = append(fields, LotterySettingsFieldError{
				prefix + ".id",
				fmt.Sprintf("奖品标识与第 %d 行重复（每行要有唯一 id）", prev+1),
			})
		} else {
			seen[id] = i
		}
		if prize.Weight > 0 {
			totalWeight += prize.Weight
		}
	}
	if len(settings.Prizes) > 0 && totalWeight <= 0 {
		fields = append(fields, LotterySettingsFieldError{
			Field:   "prizes",
			Message: "至少一个奖品的权重要大于 0，否则转盘抽不出东西",
		})
	}

	if len(fields) > 0 {
		return &LotterySettingsError{Fields: fields}
	}
	return nil
}

// GetLotterySettings 读取抽奖配置（未配置项回落默认值）。
func GetLotterySettings() LotterySettings {
	s := defaultLotterySettings()

	if v, err := model.GetOptionValue(LotteryEnabledOptionKey); err == nil && strings.TrimSpace(v) != "" {
		s.Enabled = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, err := model.GetOptionValue(LotteryRequireQQOptionKey); err == nil && strings.TrimSpace(v) != "" {
		s.RequireQQ = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, err := model.GetOptionValue(LotteryDailySpinsOptionKey); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil {
			if n < 0 {
				n = 0
			}
			if n > LotteryMaxDailySpins {
				n = LotteryMaxDailySpins
			}
			s.DailySpins = n
		}
	}
	if v, err := model.GetOptionValue(LotteryPaidEnabledOptionKey); err == nil && strings.TrimSpace(v) != "" {
		s.PaidEnabled = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, err := model.GetOptionValue(LotteryPaidPriceOptionKey); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil {
			if n < 0 {
				n = 0
			}
			if n > LotteryMaxPaidPrice {
				n = LotteryMaxPaidPrice
			}
			s.PaidPrice = n
		}
	}
	if v, err := model.GetOptionValue(LotteryPaidDailyLimitOptionKey); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil {
			if n < 0 {
				n = 0
			}
			if n > LotteryMaxPaidDailyLimit {
				n = LotteryMaxPaidDailyLimit
			}
			s.PaidDailyLimit = n
		}
	}
	if v, err := model.GetOptionValue(LotteryPrizesOptionKey); err == nil && strings.TrimSpace(v) != "" {
		var prizes []LotteryPrize
		if perr := json.Unmarshal([]byte(v), &prizes); perr == nil {
			if normalized := NormalizeLotteryPrizes(prizes); len(normalized) > 0 {
				s.Prizes = normalized
			}
		}
	}
	// 美元报价在这里统一补：默认奖池走的是 DefaultLotteryPrizes()，
	// 不会经过 NormalizeLotteryPrizes，漏掉这一步前端会显示 $0。
	s.PaidPriceUSD = common.RoundUSD(common.QuotaToUSD(int64(s.PaidPrice)))
	for i := range s.Prizes {
		if s.Prizes[i].QuotaUSD <= 0 {
			s.Prizes[i].QuotaUSD = common.RoundUSD(common.QuotaToUSD(int64(s.Prizes[i].Quota)))
		}
	}
	return s
}

// PickLotteryPrize 按权重抽一个奖（导出以便单测直接验证分布）。
func PickLotteryPrize(prizes []LotteryPrize, rnd *rand.Rand) (LotteryPrize, error) {
	total := 0
	for _, prize := range prizes {
		if prize.Weight > 0 {
			total += prize.Weight
		}
	}
	if total <= 0 {
		return LotteryPrize{}, errors.New("lottery prize pool is empty")
	}
	if rnd == nil {
		rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	target := rnd.Intn(total)
	for _, prize := range prizes {
		if prize.Weight <= 0 {
			continue
		}
		if target < prize.Weight {
			return prize, nil
		}
		target -= prize.Weight
	}
	return prizes[len(prizes)-1], nil
}

var (
	// ErrLotteryDisabled 抽奖未开放。
	ErrLotteryDisabled = errors.New("lottery is not enabled")
	// ErrLotteryQQRequired 未绑定 QQ。
	ErrLotteryQQRequired = errors.New("bind a QQ account before spinning")
	// ErrLotteryNoSpinsLeft 今日免费次数用尽。
	ErrLotteryNoSpinsLeft = errors.New("no spins left today")
	// ErrLotteryPaidDisabled 站点未开放付费抽奖。
	ErrLotteryPaidDisabled = errors.New("paid lottery is not enabled")
	// ErrLotteryPaidNoSpinsLeft 今日付费抽奖次数用尽。
	ErrLotteryPaidNoSpinsLeft = errors.New("no paid spins left today")
	// ErrLotteryInsufficientQuota 付费余额不足，无法付费抽奖。
	ErrLotteryInsufficientQuota = errors.New("insufficient paid quota for lottery")
)

// LotteryStatus 用户侧的抽奖状态。
//
// 免费与付费两条通道分开计数：付费抽奖不占用免费次数，免费次数也不会
// 因为付费抽奖被消耗（用户点「付费」就是明确要用余额）。
type LotteryStatus struct {
	Settings LotterySettings `json:"settings"`
	// SpinsUsed / SpinsRemaining 今日免费次数口径。
	SpinsUsed      int `json:"spins_used"`
	SpinsRemaining int `json:"spins_remaining"`
	// PaidSpinsUsed / PaidSpinsRemaining 今日付费次数口径。
	PaidSpinsUsed      int `json:"paid_spins_used"`
	PaidSpinsRemaining int `json:"paid_spins_remaining"`
	// Quota 工作余额（含免费部分）；PaidQuota 仅付费余额（quota - free_quota）。
	Quota int `json:"quota"`
	// PaidQuota 付费余额，付费抽奖只认这个数。
	PaidQuota int `json:"paid_quota"`
	// PaidQuotaUSD 付费余额的美元报价（页面展示口径）。
	PaidQuotaUSD float64 `json:"paid_quota_usd"`
	// PaidAffordable 现在能不能付费抽一次（前端据此禁用按钮，不用自己算）。
	PaidAffordable bool   `json:"paid_affordable"`
	QQBound        bool   `json:"qq_bound"`
	QQ             string `json:"qq"`
}

// MaskQQ 展示用脱敏：保留前 3 后 2。
func MaskQQ(qq string) string {
	runes := []rune(qq)
	if len(runes) <= 5 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:3]) + strings.Repeat("*", len(runes)-5) + string(runes[len(runes)-2:])
}

// countLotterySpinsToday 返回今日（总次数, 付费次数）。免费次数 = 总 - 付费。
func countLotterySpinsToday(userId int) (int, int, error) {
	dayKey := model.LotteryDayKey(time.Now())
	rawTotal, err := model.CountLotterySpinsOnDay(userId, dayKey)
	if err != nil {
		return 0, 0, err
	}
	rawPaid, err := model.CountLotteryPaidSpinsOnDay(userId, dayKey)
	if err != nil {
		return 0, 0, err
	}
	return int(rawTotal), int(rawPaid), nil
}

// clampNonNegative 小工具：次数相减可能为负（历史数据口径变化时）。
func clampNonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// GetLotteryStatus 汇总开关、剩余次数（免费/付费）、余额与 QQ 绑定状态。
func GetLotteryStatus(userId int) (*LotteryStatus, error) {
	settings := GetLotterySettings()
	total, paidUsed, err := countLotterySpinsToday(userId)
	if err != nil {
		return nil, err
	}
	qq, err := model.GetUserQQ(userId)
	if err != nil {
		return nil, err
	}
	quota, err := model.GetUserQuota(userId, true)
	if err != nil {
		return nil, err
	}
	freeQuota, err := model.GetUserFreeQuota(userId)
	if err != nil {
		return nil, err
	}
	freeUsed := clampNonNegative(total - paidUsed)
	paidQuota := clampNonNegative(quota - freeQuota)
	paidRemaining := clampNonNegative(settings.PaidDailyLimit - paidUsed)
	paidAffordable := settings.PaidEnabled && settings.PaidPrice > 0 &&
		paidRemaining > 0 && paidQuota >= settings.PaidPrice
	return &LotteryStatus{
		Settings:           settings,
		SpinsUsed:          freeUsed,
		SpinsRemaining:     clampNonNegative(settings.DailySpins - freeUsed),
		PaidSpinsUsed:      paidUsed,
		PaidSpinsRemaining: paidRemaining,
		Quota:              quota,
		PaidQuota:          paidQuota,
		PaidQuotaUSD:       common.RoundUSD(common.QuotaToUSD(int64(paidQuota))),
		PaidAffordable:     paidAffordable,
		QQBound:            strings.TrimSpace(qq) != "",
		QQ:                 MaskQQ(strings.TrimSpace(qq)),
	}, nil
}

// LotterySpinResult 抽奖结果（供前端播放转盘动画用 prize_index 定位扇区）。
type LotterySpinResult struct {
	Prize          LotteryPrize `json:"prize"`
	PrizeIndex     int          `json:"prize_index"`
	QuotaAwarded   int          `json:"quota_awarded"`
	SpinsUsed      int          `json:"spins_used"`
	SpinsRemaining int          `json:"spins_remaining"`
	PaidSpinsUsed  int          `json:"paid_spins_used"`
	PaidSpinsLeft  int          `json:"paid_spins_remaining"`
	// Paid 本次是否付费抽奖；QuotaCost 本次花费（免费为 0）。
	Paid      bool               `json:"paid"`
	QuotaCost int                `json:"quota_cost"`
	Spin      *model.LotterySpin `json:"spin"`
}

// SpinLottery 执行一次抽奖。paid=true 走付费通道。
//
// 付费通道的顺序是「先扣费 → 再抽奖 → 失败退款」：
//   - 扣费用 TryReserveUserQuotaUseFree(useFree=false)，条件更新保证余额不足
//     直接拒绝，绝不会像裸 decreaseUserQuota 那样把余额扣成负数；
//   - 只扣付费余额（quota - free_quota），免费额度是给免费模型的，不能拿来抽奖；
//   - 抽奖记录没落库就失败时原样退款，避免用户白花钱；
//   - 记录已落库但发奖失败时**不退款**（否则等于「有中奖记录又退钱」双花），
//     错误上抛，运营按记录里的 quota_cost 对账补偿。
func SpinLottery(userId int, paid bool) (*LotterySpinResult, error) {
	settings := GetLotterySettings()
	if !settings.Enabled {
		return nil, ErrLotteryDisabled
	}
	if paid {
		if !settings.PaidEnabled || settings.PaidPrice <= 0 || settings.PaidDailyLimit <= 0 {
			return nil, ErrLotteryPaidDisabled
		}
	} else if settings.DailySpins <= 0 {
		return nil, ErrLotteryNoSpinsLeft
	}
	if settings.RequireQQ {
		bound, err := model.IsQQBound(userId)
		if err != nil {
			return nil, err
		}
		if !bound {
			return nil, ErrLotteryQQRequired
		}
	}

	charged := 0
	if paid {
		ok, err := model.TryReserveUserQuotaUseFree(userId, settings.PaidPrice, false)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrLotteryInsufficientQuota
		}
		charged = settings.PaidPrice
	}

	result, recorded, err := spinLotteryAttempts(userId, settings, paid, charged)
	if err != nil {
		if charged > 0 && !recorded {
			if refundErr := model.IncreaseUserQuota(userId, charged, true); refundErr != nil {
				return nil, fmt.Errorf(
					"lottery charge refund failed (user=%d quota=%d): %w", userId, charged, refundErr)
			}
		}
		return nil, err
	}
	return result, nil
}

// spinLotteryAttempts 落库 + 发奖，第二个返回值表示「记录是否已落库」。
// 唯一索引冲突时重取次数重试：并发的另一个请求刚占了下标。
func spinLotteryAttempts(userId int, settings LotterySettings, paid bool, cost int) (*LotterySpinResult, bool, error) {
	now := time.Now()
	dayKey := model.LotteryDayKey(now)

	for attempt := 0; attempt < 3; attempt++ {
		total, paidUsed, err := countLotterySpinsToday(userId)
		if err != nil {
			return nil, false, err
		}
		freeUsed := clampNonNegative(total - paidUsed)
		if paid {
			if paidUsed >= settings.PaidDailyLimit {
				return nil, false, ErrLotteryPaidNoSpinsLeft
			}
		} else if freeUsed >= settings.DailySpins {
			return nil, false, ErrLotteryNoSpinsLeft
		}

		prize, idx, err := pickLotteryPrizeWithIndex(settings.Prizes)
		if err != nil {
			return nil, false, err
		}
		spin := &model.LotterySpin{
			UserId: userId,
			DayKey: dayKey,
			// spin_index 用「今日总次数」（免费+付费）而不是分通道计数：
			// 唯一索引 (user_id, day_key, spin_index) 才能同时兜住两条通道的并发。
			SpinIndex:    total,
			PrizeId:      prize.Id,
			PrizeName:    prize.Name,
			PrizeType:    prize.Type,
			QuotaAwarded: prize.Quota,
			Paid:         paid,
			QuotaCost:    cost,
			CreatedAt:    now.Unix(),
		}
		if err := model.CreateLotterySpin(spin); err != nil {
			if errors.Is(err, model.ErrLotterySpinConflict) {
				continue
			}
			return nil, false, err
		}

		awarded := 0
		if prize.Type == LotteryPrizeTypeQuota && prize.Quota > 0 {
			if err := model.IncreaseUserFreeQuota(userId, prize.Quota); err != nil {
				// 记录已落库：不再退款，避免同一笔抽奖既留记录又退钱。
				return nil, true, fmt.Errorf("prize recorded but quota grant failed: %w", err)
			}
			awarded = prize.Quota
		}

		freeRemaining := clampNonNegative(settings.DailySpins - freeUsed)
		paidRemaining := clampNonNegative(settings.PaidDailyLimit - paidUsed)
		if paid {
			paidRemaining = clampNonNegative(paidRemaining - 1)
		} else {
			freeRemaining = clampNonNegative(freeRemaining - 1)
		}
		return &LotterySpinResult{
			Prize:          prize,
			PrizeIndex:     idx,
			QuotaAwarded:   awarded,
			SpinsUsed:      freeUsed + boolToInt(!paid),
			SpinsRemaining: freeRemaining,
			PaidSpinsUsed:  paidUsed + boolToInt(paid),
			PaidSpinsLeft:  paidRemaining,
			Paid:           paid,
			QuotaCost:      cost,
			Spin:           spin,
		}, true, nil
	}
	if paid {
		return nil, false, ErrLotteryPaidNoSpinsLeft
	}
	return nil, false, ErrLotteryNoSpinsLeft
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func pickLotteryPrizeWithIndex(prizes []LotteryPrize) (LotteryPrize, int, error) {
	total := 0
	for _, prize := range prizes {
		if prize.Weight > 0 {
			total += prize.Weight
		}
	}
	if total <= 0 {
		return LotteryPrize{}, 0, errors.New("lottery prize pool is empty")
	}
	target := rand.Intn(total)
	for i, prize := range prizes {
		if prize.Weight <= 0 {
			continue
		}
		if target < prize.Weight {
			return prize, i, nil
		}
		target -= prize.Weight
	}
	return prizes[len(prizes)-1], len(prizes) - 1, nil
}

// SaveLotterySettings 保存抽奖配置（管理端）。
//
// 校验不通过时返回 *LotterySettingsError（不写库），成功后回读一次，
// 让前端拿到「实际生效」的配置而不是自己提交的草稿。
func SaveLotterySettings(settings LotterySettings) (LotterySettings, error) {
	// 美元是唯一的报价入口：管理端填 $，额度由服务端换算。
	// 老客户端只传 paid_price（额度）时 PaidPriceUSD 为 0，保持原值不动。
	if settings.PaidPriceUSD > 0 {
		settings.PaidPrice = int(common.USDToQuota(settings.PaidPriceUSD))
	}
	settings.Prizes = CanonicalizeLotteryPrizes(settings.Prizes)
	if err := ValidateLotterySettings(settings); err != nil {
		return LotterySettings{}, err
	}
	encoded, err := json.Marshal(settings.Prizes)
	if err != nil {
		return LotterySettings{}, err
	}
	if err := model.UpdateOptionsBulk(map[string]string{
		LotteryEnabledOptionKey:        strconv.FormatBool(settings.Enabled),
		LotteryDailySpinsOptionKey:     strconv.Itoa(settings.DailySpins),
		LotteryRequireQQOptionKey:      strconv.FormatBool(settings.RequireQQ),
		LotteryPrizesOptionKey:         string(encoded),
		LotteryPaidEnabledOptionKey:    strconv.FormatBool(settings.PaidEnabled),
		LotteryPaidPriceOptionKey:      strconv.Itoa(settings.PaidPrice),
		LotteryPaidDailyLimitOptionKey: strconv.Itoa(settings.PaidDailyLimit),
	}); err != nil {
		return LotterySettings{}, err
	}
	return GetLotterySettings(), nil
}

// ResetLotterySettings 恢复默认奖池与默认开关（管理端「恢复默认」）。
func ResetLotterySettings() (LotterySettings, error) {
	defaults := defaultLotterySettings()
	return SaveLotterySettings(defaults)
}
