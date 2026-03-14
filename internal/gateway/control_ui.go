package gateway

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/config"
	"goclaw/internal/domain"
)

const controlAPIPathPrefix = "/api/control/"

type controlStatusView struct {
	Snapshot   StatusSnapshot `json:"snapshot"`
	ControlURL string         `json:"control_url"`
}

type controlConfigView struct {
	Path            string         `json:"path"`
	PathOrigin      string         `json:"path_origin"`
	Exists          bool           `json:"exists"`
	ControlURL      string         `json:"control_url"`
	FileConfig      map[string]any `json:"file_config"`
	Defaults        map[string]any `json:"defaults"`
	EffectiveConfig map[string]any `json:"effective_config"`
	Warnings        []string       `json:"warnings,omitempty"`
}

type controlConfigSaveRequest struct {
	Config map[string]any `json:"config"`
}

type controlConfigSaveResponse struct {
	Saved   bool              `json:"saved"`
	Message string            `json:"message"`
	View    controlConfigView `json:"view"`
}

//go:embed ui/index.html
var controlUIFS embed.FS

var controlIndexHTML = mustReadControlIndexHTML()

func mustReadControlIndexHTML() []byte {
	data, err := controlUIFS.ReadFile("ui/index.html")
	if err != nil {
		panic(err)
	}
	return data
}

func (s *Server) controlRoutes() ([]channelcore.HTTPRoute, error) {
	if s == nil || s.App == nil || !s.controlEnabled() {
		return nil, nil
	}

	return []channelcore.HTTPRoute{
		{
			Name:    fmt.Sprintf("control[%s]", s.controlAddress()),
			Address: s.controlAddress(),
			Path:    "/",
			Handler: s.controlHandler(),
		},
	}, nil
}

func (s *Server) controlEnabled() bool {
	if s == nil || s.App == nil {
		return false
	}
	control := s.App.Config.Gateway.Control
	return control.Enabled && strings.TrimSpace(control.Host) != "" && control.Port > 0
}

func (s *Server) controlAddress() string {
	if !s.controlEnabled() {
		return ""
	}
	control := s.App.Config.Gateway.Control
	return net.JoinHostPort(strings.TrimSpace(control.Host), strconv.Itoa(control.Port))
}

func (s *Server) controlURL() string {
	if s == nil || s.App == nil {
		return ""
	}
	return ControlURLFromConfig(s.App.Config)
}

func displayControlHost(host string) string {
	trimmed := strings.TrimSpace(strings.Trim(host, "[]"))
	switch strings.ToLower(trimmed) {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return trimmed
	}
}

func (s *Server) controlHandler() http.Handler {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/control/status", s.handleControlStatus)
	apiMux.HandleFunc("/api/control/config", s.handleControlConfig)
	apiMux.HandleFunc("/api/control/channels/inspect", s.handleControlChannelInspect)
	apiMux.HandleFunc("/api/control/channels/start", s.handleControlChannelStart)
	apiMux.HandleFunc("/api/control/channels/stop", s.handleControlChannelStop)
	apiMux.HandleFunc("/api/control/channels/restart", s.handleControlChannelRestart)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, controlAPIPathPrefix) {
			apiMux.ServeHTTP(w, r)
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(controlIndexHTML)
	})
}

func (s *Server) handleControlStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeGatewayJSON(w, http.StatusOK, controlStatusView{
		Snapshot:   s.StatusSnapshot(),
		ControlURL: s.controlURL(),
	})
}

func (s *Server) handleControlConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		view, err := s.loadControlConfigView()
		if err != nil {
			writeGatewayError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeGatewayJSON(w, http.StatusOK, view)
	case http.MethodPost, http.MethodPut:
		s.handleControlConfigSave(w, r)
	default:
		writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleControlConfigSave(w http.ResponseWriter, r *http.Request) {
	var request controlConfigSaveRequest
	if err := decodeGatewayJSON(r.Body, &request); err != nil {
		writeGatewayError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Config == nil {
		writeGatewayError(w, http.StatusBadRequest, "config payload is required")
		return
	}

	editablePath, err := config.ResolveEditablePath(s.App.Config)
	if err != nil {
		writeGatewayError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := config.SaveFileMap(editablePath.Path, request.Config); err != nil {
		writeGatewayError(w, http.StatusBadRequest, err.Error())
		return
	}

	view, err := s.loadControlConfigView()
	if err != nil {
		writeGatewayError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeGatewayJSON(w, http.StatusOK, controlConfigSaveResponse{
		Saved: true,
		Message: "配置已写入文件。当前运行中的 transport、model 和 memory provider 不会自动热重载，" +
			"请重启 goclaw serve 使变更完全生效。",
		View: view,
	})
}

func (s *Server) handleControlChannelInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	selector, err := controlSelectorFromQuery(r.URL.Query())
	if err != nil {
		writeGatewayError(w, http.StatusBadRequest, err.Error())
		return
	}

	view, err := s.InspectAccount(selector)
	if err != nil {
		writeGatewayOperatorError(w, err)
		return
	}
	writeGatewayJSON(w, http.StatusOK, view)
}

func (s *Server) handleControlChannelStart(w http.ResponseWriter, r *http.Request) {
	s.handleControlLifecycleAction(w, r, LifecycleActionStart)
}

func (s *Server) handleControlChannelStop(w http.ResponseWriter, r *http.Request) {
	s.handleControlLifecycleAction(w, r, LifecycleActionStop)
}

func (s *Server) handleControlChannelRestart(w http.ResponseWriter, r *http.Request) {
	s.handleControlLifecycleAction(w, r, LifecycleActionRestart)
}

func (s *Server) handleControlLifecycleAction(
	w http.ResponseWriter,
	r *http.Request,
	action LifecycleAction,
) {
	if r.Method != http.MethodPost {
		writeGatewayError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var selector AccountSelector
	if err := decodeGatewayJSON(r.Body, &selector); err != nil {
		writeGatewayError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateControlSelector(selector); err != nil {
		writeGatewayError(w, http.StatusBadRequest, err.Error())
		return
	}

	var (
		result LifecycleActionResult
		err    error
	)
	switch action {
	case LifecycleActionStart:
		result, err = s.StartAccount(r.Context(), selector)
	case LifecycleActionStop:
		result, err = s.StopAccount(r.Context(), selector)
	case LifecycleActionRestart:
		result, err = s.RestartAccount(r.Context(), selector)
	default:
		writeGatewayError(w, http.StatusInternalServerError, fmt.Sprintf("unknown lifecycle action %q", action))
		return
	}
	if err != nil {
		writeGatewayOperatorError(w, err)
		return
	}
	writeGatewayJSON(w, http.StatusOK, result)
}

func (s *Server) loadControlConfigView() (controlConfigView, error) {
	if s == nil || s.App == nil {
		return controlConfigView{}, fmt.Errorf("gateway: missing runtime app")
	}

	editablePath, err := config.ResolveEditablePath(s.App.Config)
	if err != nil {
		return controlConfigView{}, err
	}
	defaults, err := config.DefaultMap()
	if err != nil {
		return controlConfigView{}, err
	}
	fileConfig, exists, err := config.LoadFileMap(editablePath.Path)
	if err != nil {
		return controlConfigView{}, err
	}

	effectiveConfig, err := s.loadEffectiveControlConfig(editablePath, exists)
	if err != nil {
		return controlConfigView{}, err
	}

	return controlConfigView{
		Path:            editablePath.Path,
		PathOrigin:      editablePath.Origin,
		Exists:          exists,
		ControlURL:      s.controlURL(),
		FileConfig:      fileConfig,
		Defaults:        defaults,
		EffectiveConfig: effectiveConfig,
		Warnings:        s.controlWarnings(editablePath, exists),
	}, nil
}

func (s *Server) loadEffectiveControlConfig(
	editablePath config.EditablePath,
	exists bool,
) (map[string]any, error) {
	if exists {
		return config.LoadEffectiveMap(editablePath.Path)
	}

	// If we don't have a file yet, reflect the current runtime config instead of
	// failing the UI.
	return config.ConfigToMap(s.App.Config)
}

func (s *Server) controlWarnings(editablePath config.EditablePath, exists bool) []string {
	warnings := make([]string, 0, 3)
	if editablePath.Origin == config.EditablePathOriginWorkingDirectory &&
		strings.TrimSpace(s.App.Config.SourcePath) == "" {
		if exists {
			warnings = append(
				warnings,
				"当前进程没有通过 --config 或 GOCLAW_CONFIG_PATH 指定配置文件，控制台会回退到当前工作目录下的 goclaw.json。",
			)
		} else {
			warnings = append(
				warnings,
				"当前进程没有显式配置文件路径。首次保存会在当前工作目录创建 goclaw.json。",
			)
		}
	}
	if hasGOCLAWEnvOverrides() {
		warnings = append(
			warnings,
			"检测到 GOCLAW_* 环境变量。页面里的 effective config 可能和保存到文件的值不同，环境变量仍会在运行时覆盖文件配置。",
		)
	}
	if !isLoopbackHost(s.App.Config.Gateway.Control.Host) {
		warnings = append(
			warnings,
			"控制台当前不是 loopback 绑定。这个 Web 端没有额外鉴权，暴露到非本机网络前请先放到受信任代理之后。",
		)
	}
	return warnings
}

func hasGOCLAWEnvOverrides() bool {
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if !strings.HasPrefix(key, "GOCLAW_") {
			continue
		}
		if key == "GOCLAW_CONFIG" || key == "GOCLAW_CONFIG_PATH" {
			continue
		}
		return true
	}
	return false
}

func controlSelectorFromQuery(values url.Values) (AccountSelector, error) {
	selector := AccountSelector{
		Provider:  domain.Provider(strings.TrimSpace(values.Get("provider"))),
		AccountID: strings.TrimSpace(values.Get("account_id")),
		ProfileID: domain.ProfileID(strings.TrimSpace(values.Get("profile_id"))),
	}
	if err := validateControlSelector(selector); err != nil {
		return AccountSelector{}, err
	}
	return selector, nil
}

func validateControlSelector(selector AccountSelector) error {
	if strings.TrimSpace(selector.AccountID) == "" && strings.TrimSpace(string(selector.ProfileID)) == "" {
		return fmt.Errorf("account_id or profile_id is required")
	}
	return nil
}

func writeGatewayOperatorError(w http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	message := strings.TrimSpace(err.Error())
	switch {
	case strings.Contains(message, "account_id or profile_id is required"):
		statusCode = http.StatusBadRequest
	case strings.Contains(message, "no channel account matched"):
		statusCode = http.StatusNotFound
	case strings.Contains(message, "ambiguous account target"):
		statusCode = http.StatusConflict
	}
	writeGatewayError(w, statusCode, message)
}

func isLoopbackHost(host string) bool {
	trimmed := strings.TrimSpace(strings.Trim(host, "[]"))
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, "localhost") {
		return true
	}
	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}

func decodeGatewayJSON(body io.ReadCloser, target any) error {
	defer func() {
		_ = body.Close()
	}()

	decoder := json.NewDecoder(io.LimitReader(body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	return fmt.Errorf("unexpected extra JSON content")
}

func writeGatewayJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(payload)
}

func writeGatewayError(w http.ResponseWriter, statusCode int, message string) {
	writeGatewayJSON(w, statusCode, map[string]any{
		"error": strings.TrimSpace(message),
	})
}
