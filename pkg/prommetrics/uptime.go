package prommetrics

import (
	"time"

	"github.com/QuantumNous/new-api/common"
)

// uptimeSeconds derives process uptime from common.StartTime (set at package
// init) so there is no second source of truth to keep in sync.
func uptimeSeconds() float64 {
	start := common.StartTime
	if start <= 0 {
		return 0
	}
	return time.Since(time.Unix(start, 0)).Seconds()
}
