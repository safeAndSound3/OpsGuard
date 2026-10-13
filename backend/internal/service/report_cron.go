package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type reportCron [6]map[int]bool

// Six fields: second, minute, hour, day, month, weekday (1=Sunday, 7=Saturday).
func parseReportCron(expression string) (reportCron, error) {
	var result reportCron
	fields := strings.Fields(expression)
	if len(fields) != 6 {
		return result, fmt.Errorf("Cron 必须包含秒、分、时、日、月、周六个字段")
	}
	if (fields[3] == "?") == (fields[5] == "?") {
		return result, fmt.Errorf("日和周必须且只能有一个设为 ?")
	}
	bounds := [6][2]int{{0, 59}, {0, 59}, {0, 23}, {1, 31}, {1, 12}, {1, 7}}
	for i, field := range fields {
		low, high := bounds[i][0], bounds[i][1]
		result[i] = map[int]bool{}
		if field == "?" {
			if i != 3 && i != 5 {
				return result, fmt.Errorf("? 仅可用于日和周")
			}
			field = "*"
		}
		for _, part := range strings.Split(field, ",") {
			pieces := strings.Split(part, "/")
			if len(pieces) > 2 {
				return result, fmt.Errorf("Cron 字段格式错误：%s", part)
			}
			step := 1
			if len(pieces) == 2 {
				n, e := strconv.Atoi(pieces[1])
				if e != nil || n < 1 || n > high-low+1 {
					return result, fmt.Errorf("Cron 步长无效：%s", part)
				}
				step = n
			}
			start, end := low, high
			if pieces[0] != "*" {
				ranges := strings.Split(pieces[0], "-")
				if len(ranges) > 2 {
					return result, fmt.Errorf("Cron 范围无效")
				}
				n, e := strconv.Atoi(ranges[0])
				if e != nil {
					return result, fmt.Errorf("Cron 数字无效：%s", part)
				}
				start = n
				if len(ranges) == 2 {
					n, e = strconv.Atoi(ranges[1])
					if e != nil {
						return result, fmt.Errorf("Cron 范围无效")
					}
					end = n
				} else if len(pieces) == 1 {
					end = start
				}
			}
			if start < low || end > high || start > end {
				return result, fmt.Errorf("Cron 第 %d 项范围应为 %d-%d", i+1, low, high)
			}
			for n := start; n <= end; n += step {
				result[i][n] = true
			}
		}
	}
	return result, nil
}

func (c reportCron) matches(t time.Time) bool {
	values := [6]int{t.Second(), t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday()) + 1}
	for i, v := range values {
		if !c[i][v] {
			return false
		}
	}
	return true
}
