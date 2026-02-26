package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/sfpanel/sfpanel/internal/api/response"
	"github.com/sfpanel/sfpanel/internal/monitor"
)

type DashboardHandler struct{}

func (h *DashboardHandler) GetSystemInfo(c echo.Context) error {
	hostInfo, err := monitor.GetHostInfo()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "HOST_INFO_ERROR", "Failed to get host info")
	}

	metrics, err := monitor.GetMetrics()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, "METRICS_ERROR", "Failed to get system metrics")
	}

	return response.OK(c, map[string]interface{}{
		"host":    hostInfo,
		"metrics": metrics,
	})
}

// GetMetricsHistory returns the 24-hour metrics history collected in memory.
func (h *DashboardHandler) GetMetricsHistory(c echo.Context) error {
	history := monitor.GetHistory()
	return response.OK(c, history)
}
