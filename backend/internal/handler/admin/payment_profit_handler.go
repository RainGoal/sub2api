package admin

import (
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	maxProfitReportDays = 366
	profitReportToday   = "today"
)

var (
	errProfitReportDateRangeRequired = errors.New("start_date and end_date are required")
	errProfitReportDateRangeInvalid  = errors.New("invalid date range; use YYYY-MM-DD and ensure start_date is before end_date")
	errProfitReportDateRangeTooLarge = errors.New("date range cannot exceed 366 days")
	errProfitReportPeriodInvalid     = errors.New("period must be today")
	errProfitReportPeriodWithDates   = errors.New("period cannot be combined with start_date or end_date")
)

// GetProfitReport returns separate balance and subscription profit metrics.
// GET /api/v1/admin/payment/profit
// Set period=today for the server's current day without the cash receipts breakdown.
func (h *PaymentHandler) GetProfitReport(c *gin.Context) {
	period := c.Query("period")
	start, end, err := parseProfitReportRange(c.Query("start_date"), c.Query("end_date"), period)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var report *service.ProfitReport
	if period == profitReportToday {
		report, err = h.paymentService.GetTodayProfitReport(c.Request.Context(), start)
	} else {
		report, err = h.paymentService.GetProfitReport(c.Request.Context(), start, end)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

func parseProfitReportRange(startValue, endValue, period string) (time.Time, time.Time, error) {
	if period != "" {
		if period != profitReportToday {
			return time.Time{}, time.Time{}, errProfitReportPeriodInvalid
		}
		if startValue != "" || endValue != "" {
			return time.Time{}, time.Time{}, errProfitReportPeriodWithDates
		}
		today := timezone.Today()
		return today, today.AddDate(0, 0, 1), nil
	}
	loc := timezone.Location()
	if startValue == "" && endValue == "" {
		today := timezone.Today()
		return timezone.StartOfMonth(today), today.AddDate(0, 0, 1), nil
	}
	if startValue == "" || endValue == "" {
		return time.Time{}, time.Time{}, errProfitReportDateRangeRequired
	}

	start, err := time.ParseInLocation("2006-01-02", startValue, loc)
	if err != nil {
		return time.Time{}, time.Time{}, errProfitReportDateRangeInvalid
	}
	end, err := time.ParseInLocation("2006-01-02", endValue, loc)
	if err != nil {
		return time.Time{}, time.Time{}, errProfitReportDateRangeInvalid
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errProfitReportDateRangeInvalid
	}
	if end.After(start.AddDate(0, 0, maxProfitReportDays-1)) {
		return time.Time{}, time.Time{}, errProfitReportDateRangeTooLarge
	}
	end = end.AddDate(0, 0, 1)
	return start, end, nil
}
