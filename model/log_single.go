package model

// GetUserLogById 返回用户自己的单条计费日志，other 已按「用户可见性」过滤，
// 不含 admin_info / root_info 等运营诊断字段。
func GetUserLogById(userId int, id int) (*Log, error) {
	var log Log
	if err := LOG_DB.Where("id = ? AND user_id = ?", id, userId).First(&log).Error; err != nil {
		return nil, err
	}
	log.ChannelName = ""
	log.Other = formatLogOtherJSON(log.Other, logOtherVisibilityUser)
	return &log, nil
}

// GetUserLogByRequestId 按 request id 取用户自己的日志。
// ClickHouse 日志库的 id 不是稳定自增序列，此时用 request_id 定位更可靠。
func GetUserLogByRequestId(userId int, requestId string) (*Log, error) {
	var log Log
	if err := LOG_DB.Where("request_id = ? AND user_id = ?", requestId, userId).First(&log).Error; err != nil {
		return nil, err
	}
	log.ChannelName = ""
	log.Other = formatLogOtherJSON(log.Other, logOtherVisibilityUser)
	return &log, nil
}
