package skill

import (
	"context"
	"fmt"
	"time"
)

const DateTimeSkillCode = "datetime"

// DateTimeSkill 日期时间查询技能，本地计算无外部依赖
type DateTimeSkill struct{}

func NewDateTimeSkill() *DateTimeSkill { return &DateTimeSkill{} }

func (d *DateTimeSkill) Code() string        { return DateTimeSkillCode }
func (d *DateTimeSkill) Name() string        { return "日期时间" }
func (d *DateTimeSkill) Description() string { return "查询当前日期、时间、星期等信息，示例：/skill datetime" }

func (d *DateTimeSkill) Execute(_ context.Context, _ *ExecuteRequest) (*ExecuteResult, error) {
	now := time.Now()

	weekdayCN := [...]string{"日", "一", "二", "三", "四", "五", "六"}

	output := fmt.Sprintf(
		"当前时间信息：\n日期：%s\n时间：%s\n星期：星期%s\n时区：%s\nUnix 时间戳：%d",
		now.Format("2006-01-02"),
		now.Format("15:04:05"),
		weekdayCN[now.Weekday()],
		now.Location().String(),
		now.Unix(),
	)

	return &ExecuteResult{
		SkillCode: DateTimeSkillCode,
		Output:    output,
		Data: map[string]interface{}{
			"date":      now.Format("2006-01-02"),
			"time":      now.Format("15:04:05"),
			"weekday":   now.Weekday().String(),
			"timestamp": now.Unix(),
		},
	}, nil
}
