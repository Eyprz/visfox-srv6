package nbi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

	"github.com/eyprz/visfox-srv6/iel/internal/domain"
	"github.com/eyprz/visfox-srv6/iel/internal/ingestion/usecase"
)

var statusOf = map[apitypes.ErrorCode]int{
	apitypes.ErrorCodeINVALIDREQUEST:       http.StatusBadRequest,
	apitypes.ErrorCodeINVALIDPARAMS:        http.StatusBadRequest,
	apitypes.ErrorCodeRESOURCENOTFOUND:     http.StatusNotFound,
	apitypes.ErrorCodeINVALIDCELLSTATE:     http.StatusConflict,
	apitypes.ErrorCodePRESETINUSE:          http.StatusConflict,
	apitypes.ErrorCodeINVALIDCHAIN:         http.StatusUnprocessableEntity,
	apitypes.ErrorCodeINFEASIBLEASSIGNMENT: http.StatusUnprocessableEntity,
	apitypes.ErrorCodeRESOURCEEXHAUSTED:    http.StatusConflict,
	apitypes.ErrorCodeROUTERERROR:          http.StatusBadGateway,
	apitypes.ErrorCodeROUTERTIMEOUT:        http.StatusGatewayTimeout,
	apitypes.ErrorCodeINTERNALERROR:        http.StatusInternalServerError,
	apitypes.ErrorCodeNOTIMPLEMENTED:       http.StatusNotImplemented,
}

// fail writes an ErrorResponse for err.
func fail(c *echo.Context, err error) error {
	var e *domain.Error
	if !errors.As(err, &e) {
		e = domain.Internal(err)
	}
	status, ok := statusOf[e.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	resp := apitypes.ErrorResponse{Code: e.Code, Message: e.Message}
	if e.Details != nil {
		resp.Details = &e.Details
	}
	if len(e.Rollbacks) > 0 {
		resp.Rollbacks = &e.Rollbacks
	}
	return c.JSON(status, resp)
}

func invalid(c *echo.Context, format string, args ...any) error {
	return fail(c, domain.Fail(apitypes.ErrorCodeINVALIDREQUEST, format, args...))
}

// decode reads a JSON request body.
func decode(c *echo.Context, v any) error {
	if err := json.NewDecoder(c.Request().Body).Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func reply[T any](c *echo.Context, status int) func(T, error) error {
	return func(v T, err error) error {
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(status, v)
	}
}

func ok[T any](run func() T) echo.HandlerFunc {
	return func(c *echo.Context) error { return c.JSON(http.StatusOK, run()) }
}

// ---------------------------------------------------------------- resources

// GetSourcesEndpoint serves getSources: GET /resources/sources.
func GetSourcesEndpoint(u usecase.GetSources) echo.HandlerFunc { return ok(u.Run) }

// GetDestinationsEndpoint serves getDestinations: GET /resources/destinations.
func GetDestinationsEndpoint(u usecase.GetDestinations) echo.HandlerFunc { return ok(u.Run) }

// GetSwitchersEndpoint serves getSwitchers: GET /resources/switchers.
func GetSwitchersEndpoint(u usecase.GetSwitchers) echo.HandlerFunc { return ok(u.Run) }

// GetResourcePoolsEndpoint serves getResourcePools: GET /resources/pools.
func GetResourcePoolsEndpoint(u usecase.GetResourcePools) echo.HandlerFunc { return ok(u.Run) }

// GetTopologyEndpoint serves getTopology: GET /resources/topology.
func GetTopologyEndpoint(u usecase.GetTopology) echo.HandlerFunc { return ok(u.Run) }

// ---------------------------------------------------------------- presets

// GetPresetsEndpoint serves getPresets: GET /presets.
func GetPresetsEndpoint(u usecase.GetPresets) echo.HandlerFunc { return ok(u.Run) }

// GetPresetConstraintsEndpoint serves getPresetConstraints: GET /presets/constraints.
func GetPresetConstraintsEndpoint(u usecase.GetPresetConstraints) echo.HandlerFunc { return ok(u.Run) }

func presetInput(c *echo.Context) (apitypes.PresetInput, error) {
	var in apitypes.PresetInput
	if err := decode(c, &in); err != nil {
		return in, err
	}
	if in.Name == "" || in.Chain == nil {
		return in, errors.New("name and chain are required")
	}
	return in, nil
}

// ValidatePresetEndpoint serves validatePreset: POST /presets/validate.
func ValidatePresetEndpoint(u usecase.ValidatePreset) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var in apitypes.PresetInput
		if err := decode(c, &in); err != nil {
			return invalid(c, "%v", err)
		}
		if in.Chain == nil {
			return invalid(c, "chain is required")
		}
		return c.JSON(http.StatusOK, u.Run(in))
	}
}

// CreatePresetEndpoint serves createPreset: POST /presets.
func CreatePresetEndpoint(u usecase.CreatePreset) echo.HandlerFunc {
	return func(c *echo.Context) error {
		in, err := presetInput(c)
		if err != nil {
			return invalid(c, "%v", err)
		}
		return reply[apitypes.Preset](c, http.StatusCreated)(u.Run(c.Request().Context(), in))
	}
}

// GetPresetByIDEndpoint serves getPresetById: GET /presets/{presetId}.
func GetPresetByIDEndpoint(u usecase.GetPresetByID) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.Preset](c, http.StatusOK)(u.Run(c.Param("presetId")))
	}
}

// UpdatePresetEndpoint serves updatePreset: PUT /presets/{presetId}.
func UpdatePresetEndpoint(u usecase.UpdatePreset) echo.HandlerFunc {
	return func(c *echo.Context) error {
		in, err := presetInput(c)
		if err != nil {
			return invalid(c, "%v", err)
		}
		return reply[apitypes.Preset](c, http.StatusOK)(u.Run(c.Request().Context(), c.Param("presetId"), in))
	}
}

// DeletePresetEndpoint serves deletePreset: DELETE /presets/{presetId}.
func DeletePresetEndpoint(u usecase.DeletePreset) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if err := u.Run(c.Request().Context(), c.Param("presetId")); err != nil {
			return fail(c, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func paramsInput(c *echo.Context) (map[string]any, error) {
	var in apitypes.FunctionParamsInput
	if err := decode(c, &in); err != nil {
		return nil, err
	}
	if in.Params == nil {
		return nil, errors.New("params is required")
	}
	return in.Params, nil
}

// PatchPresetFunctionParamsEndpoint serves patchPresetFunctionParams:
// PATCH /presets/{presetId}/functions/{instanceId}.
func PatchPresetFunctionParamsEndpoint(u usecase.PatchPresetFunctionParams) echo.HandlerFunc {
	return func(c *echo.Context) error {
		params, err := paramsInput(c)
		if err != nil {
			return invalid(c, "%v", err)
		}
		return reply[apitypes.PresetFunctionUpdateResult](c, http.StatusOK)(
			u.Run(c.Request().Context(), c.Param("presetId"), c.Param("instanceId"), params))
	}
}

// ---------------------------------------------------------------- functions

// GetFunctionsEndpoint serves getFunctions: GET /functions.
func GetFunctionsEndpoint(u usecase.GetFunctions) echo.HandlerFunc { return ok(u.Run) }

// GetFunctionSchemaEndpoint serves getFunctionSchema: GET /functions/{type}/schema.
func GetFunctionSchemaEndpoint(u usecase.GetFunctionSchema) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.FunctionCatalogItem](c, http.StatusOK)(u.Run(apitypes.FunctionType(c.Param("type"))))
	}
}

// ValidateFunctionParamsEndpoint serves validateFunctionParams: POST /functions/{type}/validate.
// An unknown type is 404 before the body is read.
func ValidateFunctionParamsEndpoint(schema usecase.GetFunctionSchema, u usecase.ValidateFunctionParams) echo.HandlerFunc {
	return func(c *echo.Context) error {
		t := apitypes.FunctionType(c.Param("type"))
		if _, err := schema.Run(t); err != nil {
			return fail(c, err)
		}
		params, err := paramsInput(c)
		if err != nil {
			return invalid(c, "%v", err)
		}
		return reply[apitypes.ParamValidationResult](c, http.StatusOK)(u.Run(t, params))
	}
}

// ---------------------------------------------------------------- views (SVoT)

// GetPresetLayoutEndpoint serves getPresetLayout: GET /views/presets/{presetId}/layout.
func GetPresetLayoutEndpoint(u usecase.GetPresetLayout) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.PresetLayout](c, http.StatusOK)(u.Run(c.Param("presetId")))
	}
}

// SavePresetLayoutEndpoint serves savePresetLayout: PUT /views/presets/{presetId}/layout.
func SavePresetLayoutEndpoint(u usecase.SavePresetLayout) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var l apitypes.PresetLayout
		if err := decode(c, &l); err != nil {
			return invalid(c, "%v", err)
		}
		if l.Nodes == nil {
			return invalid(c, "nodes is required")
		}
		return reply[apitypes.PresetLayout](c, http.StatusOK)(u.Run(c.Request().Context(), c.Param("presetId"), l))
	}
}

// ---------------------------------------------------------------- matrix

// GetMatrixStateEndpoint serves getMatrixState: GET /matrix.
func GetMatrixStateEndpoint(u usecase.GetMatrixState) echo.HandlerFunc { return ok(u.Run) }

// ValidateMatrixAssignmentEndpoint serves validateMatrixAssignment: POST /matrix/validate.
func ValidateMatrixAssignmentEndpoint(u usecase.ValidateMatrixAssignment) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var in apitypes.MatrixValidationRequest
		if err := decode(c, &in); err != nil {
			return invalid(c, "%v", err)
		}
		if in.CellId == "" || in.PresetId == "" {
			return invalid(c, "cell_id and preset_id are required")
		}
		return reply[apitypes.MatrixValidationResult](c, http.StatusOK)(u.Run(in.CellId, in.PresetId, deref(in.ReleaseCells)))
	}
}

// GetMatrixCellEndpoint serves getMatrixCell: GET /matrix/cells/{cellId}.
func GetMatrixCellEndpoint(u usecase.GetMatrixCell) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.MatrixCell](c, http.StatusOK)(u.Run(c.Param("cellId")))
	}
}

// TakeCellEndpoint serves takeCell: POST /matrix/cells/{cellId}/take.
func TakeCellEndpoint(u usecase.TakeCell) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.TakeResponse](c, http.StatusOK)(u.Run(c.Request().Context(), c.Param("cellId")))
	}
}

// StandbyCellEndpoint serves standbyCell: POST /matrix/cells/{cellId}/standby.
func StandbyCellEndpoint(u usecase.StandbyCell) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.MatrixCell](c, http.StatusOK)(u.Run(c.Request().Context(), c.Param("cellId")))
	}
}

// ApplyPresetToCellEndpoint serves applyPresetToCell: PUT /matrix/cells/{cellId}/preset.
func ApplyPresetToCellEndpoint(u usecase.ApplyPresetToCell) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var in apitypes.ApplyPresetRequest
		if err := decode(c, &in); err != nil {
			return invalid(c, "%v", err)
		}
		if in.PresetId == "" {
			return invalid(c, "preset_id is required")
		}
		return reply[apitypes.MatrixCell](c, http.StatusOK)(u.Run(c.Request().Context(), c.Param("cellId"), in.PresetId, deref(in.ReleaseCells)))
	}
}

// ClearCellPresetEndpoint serves clearCellPreset: DELETE /matrix/cells/{cellId}/preset.
func ClearCellPresetEndpoint(u usecase.ClearCellPreset) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.MatrixCell](c, http.StatusOK)(u.Run(c.Request().Context(), c.Param("cellId")))
	}
}

// GetCellPathEndpoint serves getCellPath: GET /matrix/cells/{cellId}/path.
func GetCellPathEndpoint(u usecase.GetCellPath) echo.HandlerFunc {
	return func(c *echo.Context) error {
		return reply[apitypes.CellPath](c, http.StatusOK)(u.Run(c.Param("cellId")))
	}
}

// ---------------------------------------------------------------- system

// GetConfigEndpoint serves getConfig: GET /config.
func GetConfigEndpoint(u usecase.GetConfig) echo.HandlerFunc { return ok(u.Run) }

// UpdateConfigEndpoint serves updateConfig: PATCH /config.
func UpdateConfigEndpoint(u usecase.UpdateConfig) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var p apitypes.DynamicConfigPatch
		if err := decode(c, &p); err != nil {
			return invalid(c, "%v", err)
		}
		return reply[apitypes.SystemConfig](c, http.StatusOK)(u.Run(c.Request().Context(), p))
	}
}

// GetLogsEndpoint serves getLogs: GET /logs.
func GetLogsEndpoint(u usecase.GetLogs) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var level *apitypes.LogLevel
		if v := c.QueryParam("level"); v != "" {
			l := apitypes.LogLevel(v)
			switch l {
			case apitypes.LogLevelDebug, apitypes.LogLevelInfo, apitypes.LogLevelWarn, apitypes.LogLevelError:
				level = &l
			default:
				return invalid(c, "unknown log level %q", v)
			}
		}
		limit := 100
		if v := c.QueryParam("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return invalid(c, "limit must be a positive integer")
			}
			limit = n
		}
		return c.JSON(http.StatusOK, u.Run(level, c.QueryParam("cell_id"), limit))
	}
}

// deref returns the slice an optional field points to (nil when absent).
func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}
