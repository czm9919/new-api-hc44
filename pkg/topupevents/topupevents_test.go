package topupevents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSubscribePublishReceive(t *testing.T) {
	events, cancel := Subscribe(1)
	t.Cleanup(cancel)

	Publish(1, Event{TradeNo: "t1", Status: "success"})
	received, ok := <-events
	assert.True(t, ok, "publish must reach a live subscriber")
	assert.Equal(t, "t1", received.TradeNo)
	assert.Equal(t, "success", received.Status)
}

func TestPublishIsUserScoped(t *testing.T) {
	events, cancel := Subscribe(1)
	t.Cleanup(cancel)

	Publish(2, Event{TradeNo: "other", Status: "pending"})
	Publish(1, Event{TradeNo: "mine", Status: "success"})

	received, ok := <-events
	assert.True(t, ok)
	assert.Equal(t, "mine", received.TradeNo, "events for other users must not leak")
}

func TestCancelRemovesSubscriber(t *testing.T) {
	events, cancel := Subscribe(3)
	assert.Equal(t, 1, SubscriberCount())
	cancel()
	cancel() // 幂等
	assert.Equal(t, 0, SubscriberCount())

	Publish(3, Event{TradeNo: "late", Status: "success"})
	select {
	case _, ok := <-events:
		assert.False(t, ok, "channel should stay open but empty after cancel")
	default:
		// 期望路径：取消后不再收到任何事件。
	}
	_ = events
}

func TestPublishNeverBlocksOnSlowSubscriber(t *testing.T) {
	events, cancel := Subscribe(4)
	t.Cleanup(cancel)
	// 超过 subscriberBuffer 的投递必须立即返回，不阻塞支付回调。
	for i := 0; i < subscriberBuffer+8; i++ {
		Publish(4, Event{TradeNo: "drop", Status: "pending"})
	}
	// 没有死锁即视为通过；队列里应只有最前面的 subscriberBuffer 条。
	drained := 0
loop:
	for {
		select {
		case _, ok := <-events:
			assert.True(t, ok)
			drained++
			continue
		default:
			break loop
		}
	}
	assert.Equal(t, subscriberBuffer, drained)
}
