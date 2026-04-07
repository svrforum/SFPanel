package featuremonitor

import (
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/monitor"
)

type Handler struct {
	Version string
}

func (h *Handler) GetSystemInfo(c echo.Context) error {
	hostInfo, err := monitor.GetHostInfo()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrHostInfoError, "Failed to get host info")
	}

	metrics, err := monitor.GetMetrics()
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMetricsError, "Failed to get system metrics")
	}

	return response.OK(c, map[string]interface{}{
		"host":    hostInfo,
		"metrics": metrics,
		"version": h.Version,
	})
}

// GetMetricsHistory returns the metrics history collected in memory.
// Optional query param: range=1h|4h|12h|24h (default 24h)
func (h *Handler) GetMetricsHistory(c echo.Context) error {
	rangeStr := c.QueryParam("range")
	history := monitor.GetHistoryRange(rangeStr)
	return response.OK(c, history)
}

// DashboardOverview combines system info and metrics history into a single response.
type DashboardOverview struct {
	Host           *monitor.HostInfo      `json:"host"`
	Metrics        *monitor.Metrics       `json:"metrics"`
	Version        string                 `json:"version"`
	MetricsHistory []monitor.MetricsPoint `json:"metrics_history"`
	UpdateInfo     *monitor.UpdateInfo    `json:"update_info,omitempty"`
}

// GetOverview returns combined system info and metrics history in a single call
// to reduce the number of API requests on dashboard initial load.
func (h *Handler) GetOverview(c echo.Context) error {
	var (
		hostInfo       *monitor.HostInfo
		metrics        *monitor.Metrics
		metricsHistory []monitor.MetricsPoint
		hostErr        error
		metricsErr     error
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		hostInfo, hostErr = monitor.GetHostInfo()
		if hostErr == nil {
			metrics, metricsErr = monitor.GetMetrics()
		}
	}()

	rangeStr := c.QueryParam("range")
	go func() {
		defer wg.Done()
		metricsHistory = monitor.GetHistoryRange(rangeStr)
	}()

	wg.Wait()

	if hostErr != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrHostInfoError, "Failed to get host info")
	}
	if metricsErr != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrMetricsError, "Failed to get system metrics")
	}

	updateInfo := monitor.GetUpdateInfo(h.Version)

	return response.OK(c, DashboardOverview{
		Host:           hostInfo,
		Metrics:        metrics,
		Version:        h.Version,
		MetricsHistory: metricsHistory,
		UpdateInfo:     &updateInfo,
	})
}
