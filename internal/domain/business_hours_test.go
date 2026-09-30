package domain

import (
	"testing"
	"time"
)

// TestBusinessHoursOpenAndNextOpening 验证工作时间判断、下一个工作时段与下次开关变化时刻按时区、多时段和日期覆盖计算。
func TestBusinessHoursOpenAndNextOpening(t *testing.T) {
	hours := BusinessHours{Enabled: true, TimeZone: "Asia/Shanghai"}
	for day := range 5 {
		hours.Weekly[day] = []BusinessHoursPeriod{{Start: "09:00", End: "18:00"}}
	}
	hours.Weekly[0] = []BusinessHoursPeriod{{Start: "13:30", End: "18:00"}, {Start: "09:00", End: "12:00"}}
	hours.Overrides = []BusinessHoursOverride{
		{Date: "2026-10-01", Periods: []BusinessHoursPeriod{}},
		{Date: "2026-10-10", Periods: []BusinessHoursPeriod{{Start: "10:00", End: "16:00"}}},
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	at := func(value string) time.Time {
		parsed, err := time.ParseInLocation("2006-01-02 15:04", value, location)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	for _, scenario := range []struct {
		name    string
		at      string
		open    bool
		next    string
		hasNext bool
		change  string
	}{
		{"周一上午时段内", "2026-09-21 09:30", true, "2026-09-21 13:30", true, "2026-09-21 12:00"},
		{"周一午休", "2026-09-21 12:00", false, "2026-09-21 13:30", true, "2026-09-21 13:30"},
		{"周一结束时刻", "2026-09-21 18:00", false, "2026-09-22 09:00", true, "2026-09-22 09:00"},
		{"周五下班后跨周末", "2026-09-25 19:00", false, "2026-09-28 09:00", true, "2026-09-28 09:00"},
		{"覆盖为休息的周四", "2026-10-01 10:00", false, "2026-10-02 09:00", true, "2026-10-02 09:00"},
		{"覆盖为上班的周六", "2026-10-10 10:00", true, "2026-10-12 09:00", true, "2026-10-10 16:00"},
		{"覆盖上班日开始前", "2026-10-10 08:00", false, "2026-10-10 10:00", true, "2026-10-10 10:00"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// 以 UTC 时刻传入，验证按企业时区换算。
			moment := at(scenario.at).UTC()
			if open := hours.Open(moment); open != scenario.open {
				t.Fatalf("Open() = %v, want %v", open, scenario.open)
			}
			next, ok := hours.NextOpening(moment)
			if ok != scenario.hasNext || (ok && !next.Equal(at(scenario.next))) {
				t.Fatalf("NextOpening() = %v, %v, want %s", next.In(location), ok, scenario.next)
			}
			if change, ok := hours.NextChange(moment); !ok || !change.Equal(at(scenario.change)) {
				t.Fatalf("NextChange() = %v, %v, want %s", change.In(location), ok, scenario.change)
			}
		})
	}
}

// TestBusinessHoursDisabledAndEmpty 验证未启用时始终处于工作时间且没有开关变化，全部休息时没有下次处理时间。
func TestBusinessHoursDisabledAndEmpty(t *testing.T) {
	hours := BusinessHours{TimeZone: "UTC"}
	moment := time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC)
	if !hours.Open(moment) {
		t.Fatal("未启用的工作时间应始终处于工作时间")
	}
	if _, ok := hours.NextChange(moment); ok {
		t.Fatal("未启用的工作时间不应有开关变化")
	}
	hours.Enabled = true
	if hours.Open(moment) {
		t.Fatal("没有任何时段时不应处于工作时间")
	}
	if _, ok := hours.NextOpening(moment); ok {
		t.Fatal("没有任何时段时不应有下次处理时间")
	}
}

// TestBusinessHoursPeriodsValid 验证时段格式、起止顺序与重叠校验。
func TestBusinessHoursPeriodsValid(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		periods []BusinessHoursPeriod
		valid   bool
	}{
		{"空列表", nil, true},
		{"全天", []BusinessHoursPeriod{{Start: "00:00", End: "24:00"}}, true},
		{"相接的两段", []BusinessHoursPeriod{{Start: "13:00", End: "18:00"}, {Start: "09:00", End: "13:00"}}, true},
		{"重叠", []BusinessHoursPeriod{{Start: "09:00", End: "13:00"}, {Start: "12:00", End: "18:00"}}, false},
		{"起止相同", []BusinessHoursPeriod{{Start: "09:00", End: "09:00"}}, false},
		{"跨午夜", []BusinessHoursPeriod{{Start: "22:00", End: "02:00"}}, false},
		{"格式错误", []BusinessHoursPeriod{{Start: "9:00", End: "18:00"}}, false},
		{"分钟越界", []BusinessHoursPeriod{{Start: "09:60", End: "18:00"}}, false},
		{"超过 24:00", []BusinessHoursPeriod{{Start: "09:00", End: "24:01"}}, false},
	} {
		if valid := BusinessHoursPeriodsValid(scenario.periods); valid != scenario.valid {
			t.Errorf("%s: BusinessHoursPeriodsValid() = %v, want %v", scenario.name, valid, scenario.valid)
		}
	}
}

// TestBusinessHoursWorkingDuration 验证工作时长只累计区间内的工作时段，跨越下班、休息日与日期覆盖，未启用时为区间长度。
func TestBusinessHoursWorkingDuration(t *testing.T) {
	hours := BusinessHours{Enabled: true, TimeZone: "Asia/Shanghai"}
	for day := range 5 {
		hours.Weekly[day] = []BusinessHoursPeriod{{Start: "09:00", End: "12:00"}, {Start: "13:00", End: "18:00"}}
	}
	hours.Overrides = []BusinessHoursOverride{{Date: "2026-09-23"}}
	shanghai := hours.Location()
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 9, day, hour, minute, 0, 0, shanghai) }
	cases := []struct {
		name     string
		from, to time.Time
		want     time.Duration
	}{
		{name: "同一时段内", from: at(21, 9, 10), to: at(21, 9, 40), want: 30 * time.Minute},
		{name: "跨午休", from: at(21, 11, 30), to: at(21, 13, 30), want: time.Hour},
		{name: "下班前转人工次日回复", from: at(21, 17, 50), to: at(22, 9, 5), want: 15 * time.Minute},
		{name: "跨覆盖为休息的日期", from: at(22, 17, 0), to: at(24, 10, 0), want: 2 * time.Hour},
		{name: "下班后转人工次日回复", from: at(21, 20, 0), to: at(22, 9, 30), want: 30 * time.Minute},
		{name: "结束早于开始", from: at(21, 10, 0), to: at(21, 9, 0), want: 0},
	}
	for _, scenario := range cases {
		if got := hours.WorkingDuration(scenario.from, scenario.to); got != scenario.want {
			t.Errorf("%s：工作时长 = %v，期望 %v", scenario.name, got, scenario.want)
		}
	}
	hours.Enabled = false
	if got := hours.WorkingDuration(at(21, 20, 0), at(22, 9, 30)); got != 13*time.Hour+30*time.Minute {
		t.Errorf("未启用时工作时长 = %v", got)
	}
}
