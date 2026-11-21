package setting

// v1.1.0 M2 · B10 单用户 / 单模型并发上限
//
// 设计取舍：
//   - 不建表、不进 Redis。并发闸门是「进程内实时计数」，Redis 往返延迟会直接
//     叠加到每一个中继请求的首字节时间上；多副本场景下每副本各自计数，
//     语义是「每副本并发上限」，与 SQL_MAX_OPEN_CONNS 等同级部署参数一致。
//   - 0 表示不限制，与既有 ModelRequestRateLimitCount 的约定保持一致，
//     运维台不填即维持当前行为，升级零风险。
//   - 阈值走 OptionMap 热更新，改完下一个请求即生效，不需要重启。
var (
	// RelayConcurrencyLimitEnabled 是总开关，关闭时中间件直接放行。
	RelayConcurrencyLimitEnabled = false
	// RelayUserConcurrencyLimit 单个用户同时进行中的中继请求上限。
	RelayUserConcurrencyLimit = 0
	// RelayModelConcurrencyLimit 单个模型同时进行中的中继请求上限。
	RelayModelConcurrencyLimit = 0
)
