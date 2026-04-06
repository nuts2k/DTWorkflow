package report

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ReportGenerator 每日报告生成器
type ReportGenerator struct {
	collector StatCollector
	sender    CardSender
	timezone  string
	skipEmpty bool
	logger    *slog.Logger
}

// NewReportGenerator 创建报告生成器
func NewReportGenerator(collector StatCollector, sender CardSender, timezone string, skipEmpty bool) *ReportGenerator {
	return &ReportGenerator{
		collector: collector,
		sender:    sender,
		timezone:  timezone,
		skipEmpty: skipEmpty,
		logger:    slog.Default(),
	}
}

// Generate 生成并发送每日报告
func (g *ReportGenerator) Generate(ctx context.Context) error {
	loc, err := time.LoadLocation(g.timezone)
	if err != nil {
		return fmt.Errorf("加载时区 %q 失败: %w", g.timezone, err)
	}

	yesterdayRange, dayBeforeRange := reportRanges(time.Now().In(loc), loc)

	// 收集昨天的统计
	stats, err := g.collector.Collect(ctx, yesterdayRange)
	if err != nil {
		return fmt.Errorf("收集昨日统计失败: %w", err)
	}

	// 空数据处理
	if stats.IsEmpty() && g.skipEmpty {
		g.logger.InfoContext(ctx, "昨日无评审活动，跳过报告发送")
		return nil
	}

	// 收集前天的统计（用于趋势对比）
	var prevStats *DailyStats
	if !stats.IsEmpty() {
		prevStats, err = g.collector.Collect(ctx, dayBeforeRange)
		if err != nil {
			// 趋势数据查询失败不阻塞报告发送
			g.logger.WarnContext(ctx, "收集前日趋势数据失败，跳过趋势对比", "error", err)
		}
	}

	// 格式化卡片并发送
	card := FormatDailyReportCard(stats, prevStats)
	if err := g.sender.SendCard(ctx, card); err != nil {
		return fmt.Errorf("发送每日报告失败: %w", err)
	}

	g.logger.InfoContext(ctx, "每日报告发送成功", "date", stats.Date, "review_count", stats.Total.ReviewCount)
	return nil
}

func reportRanges(now time.Time, loc *time.Location) (TimeRange, TimeRange) {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	dayBeforeStart := todayStart.AddDate(0, 0, -2)

	return TimeRange{Start: yesterdayStart, End: todayStart},
		TimeRange{Start: dayBeforeStart, End: yesterdayStart}
}
