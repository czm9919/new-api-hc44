package middleware

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// v1.1.0 M2 · B10 单用户 / 单模型并发上限
//
// 卡点放在 Distribute() 之后：模型名要等 distributor 解析完请求体才有，
// 上下文里由 SetupContextForSelectedChannel 写成 original_model。
// 放在这里的好处是同时覆盖自动分组、按模型限流、任务插件等所有选渠道分支，
// 且不会因为把闸门挂在 Distribute 之前而拿到空模型名。
//
// 计数用进程内 map + 一把 mutex。中继的关键路径上真正的开销是上游往返，
// 两次 map 读写远小于 1ms，换来的是零网络依赖：Redis 抖动不会变成 429 风暴。
type relayConcurrencyGate struct {
	mu     sync.Mutex
	users  map[int]int
	models map[string]int
}

var relayConcurrencyGateInstance = &relayConcurrencyGate{
	users:  make(map[int]int),
	models: make(map[string]int),
}

// acquire 尝试占用一个并发额度。返回 nil 表示超限，reason 说明是用户维度
// 还是模型维度先撞线，方便日志与前端提示定位。
func (g *relayConcurrencyGate) acquire(userId int, modelName string, userLimit, modelLimit int) (func(), string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if userLimit > 0 && g.users[userId] >= userLimit {
		return nil, "user"
	}
	if modelName != "" && modelLimit > 0 && g.models[modelName] >= modelLimit {
		return nil, "model"
	}

	// 只给真正占用过的维度加计数：某人只受模型限制时，用户表里不留零值键，
	// 避免长期运行后 map 里堆积整站用户 id。
	if userLimit > 0 {
		g.users[userId]++
	}
	if modelName != "" && modelLimit > 0 {
		g.models[modelName]++
	}

	// 闭包捕获本次 acquire 时的阈值：请求执行期间运维台把上限从 5 改成 10，
	// 释放时仍然按 5 的口径回退，计数不会被改成负数。
	var once sync.Once
	release := func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if userLimit > 0 {
				if g.users[userId] <= 1 {
					delete(g.users, userId)
				} else {
					g.users[userId]--
				}
			}
			if modelName != "" && modelLimit > 0 {
				if g.models[modelName] <= 1 {
					delete(g.models, modelName)
				} else {
					g.models[modelName]--
				}
			}
		})
	}
	return release, ""
}

// inFlight 用于测试与排障输出，返回当前全局占用快照。
func (g *relayConcurrencyGate) inFlight() (users, models int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.users), len(g.models)
}

// RelayConcurrencyLimit 是挂在 relayV1Router 上、位于 Distribute 之后的闸门。
// 超限返回 429 + OpenAI 兼容错误体，让客户端按「稍后重试」处理；
// 同时带上 Retry-After，避免 SDK 立刻重打把队列打得更长。
func RelayConcurrencyLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		if !setting.RelayConcurrencyLimitEnabled {
			c.Next()
			return
		}

		userLimit := setting.RelayUserConcurrencyLimit
		modelLimit := setting.RelayModelConcurrencyLimit
		if userLimit <= 0 && modelLimit <= 0 {
			c.Next()
			return
		}

		userId := c.GetInt("id")
		modelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

		release, exceeded := relayConcurrencyGateInstance.acquire(userId, modelName, userLimit, modelLimit)
		if release == nil {
			c.Header("Retry-After", "1")
			message := fmt.Sprintf("Concurrency limit reached for user (limit %d)", userLimit)
			if exceeded == "model" {
				message = fmt.Sprintf("Concurrency limit reached for model %s (limit %d)", modelName, modelLimit)
			}
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, message)
			return
		}
		defer release()

		c.Next()
	}
}
