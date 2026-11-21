// Package topupevents 提供充值订单状态变更的进程内发布/订阅（v1.1.0 M1 · B14）。
//
// 背景：仓库里现成的 SSE 通道全部服务于上游流式中继，订单状态只能靠前端
// 自己轮询。B14 要求后端主动推送订单状态。发布点在 model 层的各状态迁移
// 函数里；消费点是 controller 的 SSE 流。
//
// 注意多实例部署的边界：支付回调可能落在另一台实例上，进程内 hub 推不到
// 本实例的订阅者。因此 SSE 端还叠加了一个按快照 diff 的低频兜底刷新，
// 数据永远以数据库为准，hub 只是把「本实例完成的迁移」从 2 秒级延迟压到
// 毫秒级。发布采用非阻塞投递，单个订阅者积压不影响其它订阅者，也不会
// 反压到支付回调路径。
package topupevents

import "sync"

// Event 是推给前端的最小订单快照：只含展示所需字段，不含任何敏感信息。
type Event struct {
	TradeNo      string  `json:"trade_no"`
	Status       string  `json:"status"`
	Amount       int64   `json:"amount"`
	Money        float64 `json:"money"`
	CreateTime   int64   `json:"create_time"`
	CompleteTime int64   `json:"complete_time"`
}

const subscriberBuffer = 8

var (
	mu          sync.Mutex
	subscribers = map[int]map[chan Event]struct{}{}
)

// Subscribe 注册一个针对指定用户的订阅者，返回只读事件通道与取消函数。
// 取消函数幂等，连接断开时必须调用以免订阅者泄漏。
func Subscribe(userId int) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	mu.Lock()
	perUser, ok := subscribers[userId]
	if !ok {
		perUser = make(map[chan Event]struct{})
		subscribers[userId] = perUser
	}
	perUser[ch] = struct{}{}
	mu.Unlock()
	cancel := func() {
		mu.Lock()
		if perUser, ok := subscribers[userId]; ok {
			delete(perUser, ch)
			if len(perUser) == 0 {
				delete(subscribers, userId)
			}
		}
		mu.Unlock()
	}
	return ch, cancel
}

// Publish 广播一条状态变更。通道满时丢弃该订阅者的这一条事件：
// SSE 端的快照兜底会在下一次刷新时把它追平，不能因为推送阻塞支付回调。
func Publish(userId int, event Event) {
	mu.Lock()
	perUser, ok := subscribers[userId]
	targets := make([]chan Event, 0, len(perUser))
	for ch := range perUser {
		targets = append(targets, ch)
	}
	mu.Unlock()
	if !ok {
		return
	}
	for _, ch := range targets {
		select {
		case ch <- event:
		default:
		}
	}
}

// SubscriberCount 返回当前活跃订阅者总数（运维/测试用）。
func SubscriberCount() int {
	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, perUser := range subscribers {
		total += len(perUser)
	}
	return total
}
