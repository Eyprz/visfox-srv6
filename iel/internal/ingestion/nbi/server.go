// Package nbi is the northbound interface of Intent Ingestion (RFC 9315 §5.1.1): the Echo server
// with one endpoint per operation of api/openapi.yaml (REST and the SSE stream). Endpoints only
// translate HTTP to and from the use cases (internal/ingestion/usecase).
package nbi

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/eyprz/visfox-srv6/iel/internal/assurance/report"
	"github.com/eyprz/visfox-srv6/iel/internal/ingestion/usecase"
)

// BasePath is the API prefix (api/openapi.yaml servers).
const BasePath = "/api/v1"

// New returns the Echo instance with every route of api/openapi.yaml registered.
func New(logger *slog.Logger, started time.Time, uc *usecase.UseCases, events *report.Broker) *echo.Echo {
	e := echo.New()
	e.Logger = logger
	e.Use(middleware.Recover())

	api := e.Group(BasePath)
	api.GET("/healthz", GetHealthzEndpoint(started))

	api.GET("/resources/sources", GetSourcesEndpoint(uc.GetSources))
	api.GET("/resources/destinations", GetDestinationsEndpoint(uc.GetDestinations))
	api.GET("/resources/switchers", GetSwitchersEndpoint(uc.GetSwitchers))
	api.GET("/resources/pools", GetResourcePoolsEndpoint(uc.GetResourcePools))
	api.GET("/resources/topology", GetTopologyEndpoint(uc.GetTopology))

	api.GET("/presets", GetPresetsEndpoint(uc.GetPresets))
	api.POST("/presets", CreatePresetEndpoint(uc.CreatePreset))
	api.GET("/presets/constraints", GetPresetConstraintsEndpoint(uc.GetPresetConstraints))
	api.POST("/presets/validate", ValidatePresetEndpoint(uc.ValidatePreset))
	api.GET("/presets/:presetId", GetPresetByIDEndpoint(uc.GetPresetByID))
	api.PUT("/presets/:presetId", UpdatePresetEndpoint(uc.UpdatePreset))
	api.DELETE("/presets/:presetId", DeletePresetEndpoint(uc.DeletePreset))
	api.PATCH("/presets/:presetId/functions/:instanceId", PatchPresetFunctionParamsEndpoint(uc.PatchPresetFunctionParams))

	api.GET("/functions", GetFunctionsEndpoint(uc.GetFunctions))
	api.GET("/functions/:type/schema", GetFunctionSchemaEndpoint(uc.GetFunctionSchema))
	api.POST("/functions/:type/validate", ValidateFunctionParamsEndpoint(uc.GetFunctionSchema, uc.ValidateFunctionParams))

	api.GET("/views/presets/:presetId/layout", GetPresetLayoutEndpoint(uc.GetPresetLayout))
	api.PUT("/views/presets/:presetId/layout", SavePresetLayoutEndpoint(uc.SavePresetLayout))

	api.GET("/matrix", GetMatrixStateEndpoint(uc.GetMatrixState))
	api.POST("/matrix/validate", ValidateMatrixAssignmentEndpoint(uc.ValidateMatrixAssignment))
	api.GET("/matrix/cells/:cellId", GetMatrixCellEndpoint(uc.GetMatrixCell))
	api.POST("/matrix/cells/:cellId/take", TakeCellEndpoint(uc.TakeCell))
	api.POST("/matrix/cells/:cellId/standby", StandbyCellEndpoint(uc.StandbyCell))
	api.PUT("/matrix/cells/:cellId/preset", ApplyPresetToCellEndpoint(uc.ApplyPresetToCell))
	api.DELETE("/matrix/cells/:cellId/preset", ClearCellPresetEndpoint(uc.ClearCellPreset))
	api.GET("/matrix/cells/:cellId/path", GetCellPathEndpoint(uc.GetCellPath))

	api.GET("/events", StreamEventsEndpoint(events))

	api.GET("/config", GetConfigEndpoint(uc.GetConfig))
	api.PATCH("/config", UpdateConfigEndpoint(uc.UpdateConfig))
	api.GET("/logs", GetLogsEndpoint(uc.GetLogs))
	return e
}
