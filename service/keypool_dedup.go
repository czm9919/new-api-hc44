package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 号池贡献判重的原因码。
const (
	// PoolKeyConflictDuplicateKey 同一把上游密钥已经在号池里
	PoolKeyConflictDuplicateKey = "duplicate_key"
	// PoolKeyConflictDuplicateAccount 同一个上游账号已经有密钥在号池里
	PoolKeyConflictDuplicateAccount = "duplicate_account"
)

// PoolKeyConflict 一条被拦截的贡献记录。
type PoolKeyConflict struct {
	// KeyMask 本次提交的密钥（已脱敏）
	KeyMask string `json:"key_mask"`
	// Reason duplicate_key / duplicate_account
	Reason string `json:"reason"`
	// Account 涉及的账号标识（原始写法，便于提示用户）
	Account string `json:"account"`
	// ContributorId 已占用该账号/密钥的用户 ID
	ContributorId int `json:"contributor_id"`
	// ExistingMask 已在号池里的那把密钥（已脱敏）
	ExistingMask string `json:"existing_mask"`
}

// poolChannelTag 号池渠道的 tag 前缀（keypool-<provider>）。
func poolChannelTag(provider KeyPoolProvider) string {
	return "keypool-" + string(provider)
}

// collectExistingPoolKeyHashes 收集号池渠道里已存在的密钥指纹。
//
// 台账是 hc22 才引入的，历史贡献的 key 没有记录；这里用渠道里真实的 key 兜底，
// 保证「这把 key 已经在号池里」这个判断对老数据同样成立。
func collectExistingPoolKeyHashes(provider KeyPoolProvider) map[string]string {
	hashes := make(map[string]string)
	channels, err := model.GetChannelsByTag(poolChannelTag(provider), false, true)
	if err != nil {
		return hashes
	}
	for _, ch := range channels {
		for _, key := range ch.GetKeys() {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			hashes[model.PoolKeyHash(key)] = maskKey(key)
		}
	}
	return hashes
}

// CheckPoolKeyConflicts 检查本次提交的密钥是否与号池已有贡献冲突。
//
// 两类冲突：
//  1. duplicate_key：同一把密钥已经在号池（台账或渠道里已存在）；
//  2. duplicate_account：同一个上游账号已经有另一把密钥在号池——同一账号新建的第二把
//     key 并不会增加上游的真实额度，只是把同一账号重复计入号池，必须拦截。
//
// accountKey 为空时只做密钥级判重（运营侧后台发放走这条路径）。
func CheckPoolKeyConflicts(provider KeyPoolProvider, keys []string, accountKey string) ([]PoolKeyConflict, error) {
	conflicts := make([]PoolKeyConflict, 0)
	existingHashes := collectExistingPoolKeyHashes(provider)
	accountChecked := false

	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		hash := model.PoolKeyHash(key)

		// 1) 密钥级判重：先查台账（有贡献者信息），再查渠道真实密钥（老数据兜底）。
		rec, err := model.GetPoolKeyRecordByHash(string(provider), hash)
		if err != nil {
			return nil, err
		}
		if rec != nil && rec.Status == 1 {
			conflicts = append(conflicts, PoolKeyConflict{
				KeyMask:       maskKey(key),
				Reason:        PoolKeyConflictDuplicateKey,
				Account:       rec.AccountLabel,
				ContributorId: rec.ContributorId,
				ExistingMask:  rec.KeyMask,
			})
			continue
		}
		if _, ok := existingHashes[hash]; ok {
			conflicts = append(conflicts, PoolKeyConflict{
				KeyMask:      maskKey(key),
				Reason:       PoolKeyConflictDuplicateKey,
				ExistingMask: maskKey(key),
			})
			continue
		}

		// 2) 账号级判重：同一账号只允许一把密钥占用号池名额。
		//    同一批提交里多把 key 属于同一账号时，只需要报一次账号冲突即可，
		//    因此用 accountChecked 短路，避免刷出一堆重复提示。
		if accountKey == "" || accountChecked {
			continue
		}
		accountRec, err := model.GetActivePoolKeyRecordByAccount(string(provider), accountKey, hash)
		if err != nil {
			return nil, err
		}
		if accountRec != nil {
			accountChecked = true
			conflicts = append(conflicts, PoolKeyConflict{
				KeyMask:       maskKey(key),
				Reason:        PoolKeyConflictDuplicateAccount,
				Account:       accountRec.AccountLabel,
				ContributorId: accountRec.ContributorId,
				ExistingMask:  accountRec.KeyMask,
			})
		}
	}

	return conflicts, nil
}

// FormatPoolKeyConflicts 把冲突列表拼成一条可直接返回给用户的错误信息。
func FormatPoolKeyConflicts(conflicts []PoolKeyConflict) error {
	if len(conflicts) == 0 {
		return nil
	}
	lines := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		switch c.Reason {
		case PoolKeyConflictDuplicateAccount:
			account := c.Account
			if account == "" {
				account = "（未填写）"
			}
			if c.ContributorId > 0 {
				lines = append(lines, fmt.Sprintf(
					"上游账号 %s 已贡献过密钥（%s，贡献者 #%d），同一账号只能贡献一次；请改用其他账号的密钥",
					account, c.ExistingMask, c.ContributorId))
			} else {
				lines = append(lines, fmt.Sprintf(
					"上游账号 %s 已贡献过密钥（%s），同一账号只能贡献一次；请改用其他账号的密钥",
					account, c.ExistingMask))
			}
		default:
			if c.ContributorId > 0 {
				lines = append(lines, fmt.Sprintf(
					"密钥 %s 已在号池中（贡献者 #%d），同一把密钥不能重复贡献", c.KeyMask, c.ContributorId))
			} else {
				lines = append(lines, fmt.Sprintf(
					"密钥 %s 已在号池中，同一把密钥不能重复贡献", c.KeyMask))
			}
		}
	}
	return fmt.Errorf("%s", strings.Join(lines, "；"))
}

// RecordPoolContributions 把本次通过的密钥写入贡献台账（含上游账号标识）。
//
// 台账写入失败不影响已经完成的并池结果（密钥已经进了渠道），只记日志：
// 丢了台账最多让下一次判重退化到「渠道密钥兜底」，不会影响号池可用性。
func RecordPoolContributions(provider KeyPoolProvider, channelId int, contributorId int, accountLabel string, keys []string) {
	accountLabel = strings.TrimSpace(accountLabel)
	accountKey := model.NormalizePoolAccountKey(accountLabel)
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		rec := &model.PoolKeyRecord{
			Provider:      string(provider),
			KeyHash:       model.PoolKeyHash(key),
			KeyMask:       maskKey(key),
			AccountKey:    accountKey,
			AccountLabel:  accountLabel,
			ContributorId: contributorId,
			ChannelId:     channelId,
			CreatedTime:   common.GetTimestamp(),
			Status:        1,
		}
		if err := model.UpsertPoolKeyRecord(rec); err != nil {
			common.SysError(common.LogText("failed to record pool key contribution: %s", err.Error()))
		}
	}
}

// PoolKeyAccountSummary 后台查看「某供应商下已被占用的上游账号」。
type PoolKeyAccountSummary struct {
	Provider      string `json:"provider"`
	Account       string `json:"account"`
	AccountKey    string `json:"account_key"`
	KeyMask       string `json:"key_mask"`
	ContributorId int    `json:"contributor_id"`
	ChannelId     int    `json:"channel_id"`
	CreatedTime   int64  `json:"created_time"`
	Status        int    `json:"status"`
}

// ListPoolKeyAccounts 返回某供应商已登记的上游账号（后台运营面板用）。
func ListPoolKeyAccounts(provider string) ([]PoolKeyAccountSummary, error) {
	records, err := model.ListPoolKeyRecords(provider, 200)
	if err != nil {
		return nil, err
	}
	return toPoolKeyAccountSummaries(records), nil
}

// ListUserPoolKeyAccounts 返回某个用户自己贡献过的上游账号与密钥（用户侧展示用，
// 只返回脱敏密钥，且不含其他贡献者信息）。
func ListUserPoolKeyAccounts(userId int, provider string) ([]PoolKeyAccountSummary, error) {
	records, err := model.ListPoolKeyRecordsByContributor(userId, provider, 100)
	if err != nil {
		return nil, err
	}
	return toPoolKeyAccountSummaries(records), nil
}

func toPoolKeyAccountSummaries(records []model.PoolKeyRecord) []PoolKeyAccountSummary {
	out := make([]PoolKeyAccountSummary, 0, len(records))
	for _, r := range records {
		out = append(out, PoolKeyAccountSummary{
			Provider:      r.Provider,
			Account:       r.AccountLabel,
			AccountKey:    r.AccountKey,
			KeyMask:       r.KeyMask,
			ContributorId: r.ContributorId,
			ChannelId:     r.ChannelId,
			CreatedTime:   r.CreatedTime,
			Status:        r.Status,
		})
	}
	return out
}
