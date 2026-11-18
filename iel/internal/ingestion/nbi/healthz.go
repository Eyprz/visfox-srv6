package nbi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"
)

// Version is the IEL version reported by /healthz.
const Version = "0.2.1"

// GetHealthzEndpoint serves getHealthz: GET /healthz. started is the process start time.
func GetHealthzEndpoint(started time.Time) echo.HandlerFunc {
	return func(c *echo.Context) error {
		uptime := int(time.Since(started).Seconds())
		return c.JSON(http.StatusOK, apitypes.ServiceHealth{
			Status: apitypes.ServiceHealthStatusOk, Version: Version, UptimeS: &uptime,
		})
	}
}
