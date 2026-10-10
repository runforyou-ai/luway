package domain

import (
	"slices"
	"time"
)

const (
	// BusinessHoursLookaheadDays 是计算下一个工作时段时向后查找的最大天数。
	BusinessHoursLookaheadDays = 366
	// BusinessHoursDateLayout 是日期覆盖使用的日期格式。
	BusinessHoursDateLayout = "2006-01-02"
	// businessHoursDayMinutes 是一天的分钟数，时段结束可取 24:00。
	businessHoursDayMinutes = 24 * 60
)

// BusinessHoursPeriod 是一天内的一个工作时段，起止为 HH:mm，结束可取 24:00，起始早于结束。
type BusinessHoursPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// BusinessHoursOverride 按日期覆盖当天的工作时段，Periods 为空表示当天休息。
type BusinessHoursOverride struct {
	Date    string                `json:"date"`
	Periods []BusinessHoursPeriod `json:"periods"`
}

// BusinessHours 是企业客服工作时间；Weekly 从周一到周日排列，空列表表示当天休息。
type BusinessHours struct {
	Enabled   bool
	TimeZone  string
	Weekly    [7][]BusinessHoursPeriod
	Overrides []BusinessHoursOverride
}

// BusinessHoursClockMinutes 把 HH:mm 解析为当天的分钟数，接受 00:00 至 24:00。
func BusinessHoursClockMinutes(value string) (int, bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	digits := [4]int{}
	for index, position := range [4]int{0, 1, 3, 4} {
		if value[position] < '0' || value[position] > '9' {
			return 0, false
		}
		digits[index] = int(value[position] - '0')
	}
	hour, minute := digits[0]*10+digits[1], digits[2]*10+digits[3]
	minutes := hour*60 + minute
	if minute > 59 || minutes > businessHoursDayMinutes {
		return 0, false
	}
	return minutes, true
}

// BusinessHoursPeriodsValid 判断一天的时段均合法、起始早于结束且互不重叠。
func BusinessHoursPeriodsValid(periods []BusinessHoursPeriod) bool {
	ranges := make([][2]int, 0, len(periods))
	for _, period := range periods {
		start, startOK := BusinessHoursClockMinutes(period.Start)
		end, endOK := BusinessHoursClockMinutes(period.End)
		if !startOK || !endOK || start >= end {
			return false
		}
		ranges = append(ranges, [2]int{start, end})
	}
	slices.SortFunc(ranges, func(left, right [2]int) int { return left[0] - right[0] })
	for index := 1; index < len(ranges); index++ {
		if ranges[index][0] < ranges[index-1][1] {
			return false
		}
	}
	return true
}

// Location 返回工作时间使用的时区，无法加载时返回 UTC。
func (h BusinessHours) Location() *time.Location {
	location, err := time.LoadLocation(h.TimeZone)
	if err != nil {
		return time.UTC
	}
	return location
}

// periodsOn 返回指定本地日期适用的工作时段，日期覆盖优先于每周时段。
func (h BusinessHours) periodsOn(date time.Time) []BusinessHoursPeriod {
	key := date.Format(BusinessHoursDateLayout)
	for _, override := range h.Overrides {
		if override.Date == key {
			return override.Periods
		}
	}
	return h.Weekly[(int(date.Weekday())+6)%7]
}

// Open 判断指定时刻是否处于工作时间；未启用工作时间时始终返回 true。
func (h BusinessHours) Open(at time.Time) bool {
	if !h.Enabled {
		return true
	}
	local := at.In(h.Location())
	minute := local.Hour()*60 + local.Minute()
	for _, period := range h.periodsOn(local) {
		start, _ := BusinessHoursClockMinutes(period.Start)
		end, _ := BusinessHoursClockMinutes(period.End)
		if start <= minute && minute < end {
			return true
		}
	}
	return false
}

// NextOpening 返回指定时刻之后最近一个工作时段的开始时间，向后查找 BusinessHoursLookaheadDays 天仍没有时返回 false。
func (h BusinessHours) NextOpening(at time.Time) (time.Time, bool) {
	location := h.Location()
	local := at.In(location)
	for offset := 0; offset <= BusinessHoursLookaheadDays; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+offset, 0, 0, 0, 0, location)
		starts := make([]int, 0, 2)
		for _, period := range h.periodsOn(day) {
			if start, ok := BusinessHoursClockMinutes(period.Start); ok {
				starts = append(starts, start)
			}
		}
		slices.Sort(starts)
		for _, start := range starts {
			opening := time.Date(day.Year(), day.Month(), day.Day(), start/60, start%60, 0, 0, location)
			if opening.After(at) {
				return opening, true
			}
		}
	}
	return time.Time{}, false
}

// NextChange 返回指定时刻之后最近一个工作时段的开始或结束时间，即工作时间开关状态可能变化的时刻；未启用工作时间或向后查找 BusinessHoursLookaheadDays 天仍没有时返回 false。
func (h BusinessHours) NextChange(at time.Time) (time.Time, bool) {
	if !h.Enabled {
		return time.Time{}, false
	}
	location := h.Location()
	local := at.In(location)
	for offset := 0; offset <= BusinessHoursLookaheadDays; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+offset, 0, 0, 0, 0, location)
		var next time.Time
		for _, period := range h.periodsOn(day) {
			for _, clock := range []string{period.Start, period.End} {
				minutes, ok := BusinessHoursClockMinutes(clock)
				if !ok {
					continue
				}
				boundary := time.Date(day.Year(), day.Month(), day.Day(), minutes/60, minutes%60, 0, 0, location)
				if boundary.After(at) && (next.IsZero() || boundary.Before(next)) {
					next = boundary
				}
			}
		}
		if !next.IsZero() {
			return next, true
		}
	}
	return time.Time{}, false
}

// WorkingDuration 返回 from 到 to 之间落在工作时段内的时长；未启用工作时间时为两者之差，to 不晚于 from 时为 0。
func (h BusinessHours) WorkingDuration(from, to time.Time) time.Duration {
	if !to.After(from) {
		return 0
	}
	if !h.Enabled {
		return to.Sub(from)
	}
	location := h.Location()
	local := from.In(location)
	var total time.Duration
	for day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location); day.Before(to); day = day.AddDate(0, 0, 1) {
		for _, period := range h.periodsOn(day) {
			start, startOK := BusinessHoursClockMinutes(period.Start)
			end, endOK := BusinessHoursClockMinutes(period.End)
			if !startOK || !endOK {
				continue
			}
			// 时段与区间的交集计入总时长。
			opening := time.Date(day.Year(), day.Month(), day.Day(), start/60, start%60, 0, 0, location)
			closing := time.Date(day.Year(), day.Month(), day.Day(), end/60, end%60, 0, 0, location)
			if opening.Before(from) {
				opening = from
			}
			if closing.After(to) {
				closing = to
			}
			if closing.After(opening) {
				total += closing.Sub(opening)
			}
		}
	}
	return total
}
