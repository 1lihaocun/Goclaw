package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/runtime"
	workspacectx "goclaw/internal/workspace"
)

const shutdownTimeout = 5 * time.Second

type Server struct {
	App                *runtime.App
	Logger             *slog.Logger
	channelManager     *ChannelManager
	transportInventory *TransportInventory
}

func NewServer(app *runtime.App) *Server {
	logger := slog.Default()
	channelManager := newChannelManagerForApp(app)
	return &Server{
		App:                app,
		Logger:             logger,
		channelManager:     channelManager,
		transportInventory: newTransportInventoryForApp(app, channelManager, logger),
	}
}

func (s *Server) HTTPHandler() (http.Handler, error) {
	if s == nil || s.App == nil {
		return nil, errors.New("gateway: missing runtime app")
	}

	logger := s.logger()
	buildParams := s.App.NewBuildRuntimeParams(logger)
	routes, err := s.App.Channels.HTTPRoutes(buildParams)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, errors.New("gateway: no channel webhook routes are enabled")
	}

	transports, err := buildHTTPServerTransports(routes, logger)
	if err != nil {
		return nil, err
	}
	if len(transports) != 1 {
		return nil, fmt.Errorf("gateway: HTTP handler requires exactly one webhook listener, got %d", len(transports))
	}

	serverTransport, ok := transports[0].(*httpServerTransport)
	if !ok {
		return nil, errors.New("gateway: unexpected webhook transport type")
	}
	return serverTransport.handler, nil
}

func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.App == nil {
		return errors.New("gateway: missing runtime app")
	}

	logger := s.logger()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	restartPolicy := RestartPolicy{}
	if s.App != nil {
		restartPolicy = newRestartPolicy(s.App.Config.Gateway.Restart)
	}
	errBufferSize := 4
	if s.App != nil && s.App.Channels != nil {
		errBufferSize += len(s.App.Channels.AccountSnapshots())
	}
	errCh := make(chan error, errBufferSize)
	buildParams := s.App.NewBuildRuntimeParams(logger)
	buildParams.RootContext = runCtx
	buildParams.ManagedRuntimePolicy = managedRuntimePolicyFromRestart(restartPolicy)
	buildParams.ManagedRuntimeObserver = managedRuntimeObserver{
		manager:   s.channelManager,
		inventory: s.transportInventory,
		cancel:    cancel,
		errCh:     errCh,
	}
	s.App.Channels.BindRuntime(buildParams)

	routes, err := s.App.Channels.HTTPRoutes(buildParams)
	if err != nil {
		return err
	}
	controlRoutes, err := s.controlRoutes()
	if err != nil {
		return err
	}
	routes = append(routes, controlRoutes...)
	httpTransports, err := buildHTTPServerTransports(routes, logger)
	if err != nil {
		return err
	}
	backgroundTransports, err := s.App.Channels.Transports(buildParams)
	if err != nil {
		return err
	}
	managedAccounts, err := s.startManagedAccounts(runCtx)
	if err != nil {
		return err
	}

	transports := make([]channelcore.Transport, 0, len(httpTransports)+len(backgroundTransports)+2)
	channelTransports := make([]channelcore.Transport, 0, len(httpTransports)+len(backgroundTransports))
	channelTransports = append(channelTransports, httpTransports...)
	channelTransports = append(channelTransports, backgroundTransports...)
	transports = append(transports, s.wrapManagedTransports(channelTransports)...)
	transports = append(transports, s.wrapTrackedTransports(buildWorkerTransports(s.App, logger))...)
	if len(transports) == 0 && managedAccounts == 0 {
		return errors.New("gateway: no enabled channel transports or control routes")
	}

	return runGatewayTransports(runCtx, cancel, transports, errCh)
}

func (s *Server) logger() *slog.Logger {
	if s != nil && s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func newChannelManagerForApp(app *runtime.App) *ChannelManager {
	if app == nil {
		return NewChannelManager(nil)
	}
	manager := NewChannelManager(app.Channels)
	manager.ApplyRestartPolicy(newRestartPolicy(app.Config.Gateway.Restart))
	return manager
}

func (s *Server) ChannelManager() *ChannelManager {
	if s == nil {
		return nil
	}
	return s.channelManager
}

func (s *Server) TransportInventory() *TransportInventory {
	if s == nil {
		return nil
	}
	return s.transportInventory
}

func (s *Server) wrapManagedTransports(transports []channelcore.Transport) []channelcore.Transport {
	if s == nil || s.channelManager == nil || !s.channelManager.HasAccounts() {
		return transports
	}

	policy := RestartPolicy{}
	if s.App != nil {
		policy = newRestartPolicy(s.App.Config.Gateway.Restart)
	}

	wrapped := make([]channelcore.Transport, 0, len(transports))
	for _, transport := range transports {
		if transport == nil {
			continue
		}
		wrapped = append(wrapped, managedTransport{
			Transport:     transport,
			manager:       s.channelManager,
			inventory:     s.transportInventory,
			restartPolicy: policy,
		})
	}
	return wrapped
}

func (s *Server) wrapTrackedTransports(transports []channelcore.Transport) []channelcore.Transport {
	if s == nil || s.transportInventory == nil {
		return transports
	}

	wrapped := make([]channelcore.Transport, 0, len(transports))
	for _, transport := range transports {
		if transport == nil {
			continue
		}
		wrapped = append(wrapped, trackedTransport{
			Transport: transport,
			inventory: s.transportInventory,
		})
	}
	return wrapped
}

type managedTransport struct {
	channelcore.Transport
	manager       *ChannelManager
	inventory     *TransportInventory
	restartPolicy RestartPolicy
}

func (t managedTransport) Name() string {
	if t.Transport == nil {
		return ""
	}
	return t.Transport.Name()
}

func (t managedTransport) Run(ctx context.Context) error {
	if t.Transport == nil {
		return nil
	}
	runtimeID := t.Transport.Name()
	restartCount := 0
	for {
		now := time.Now()
		if t.manager != nil {
			t.manager.MarkRuntimeStarting(runtimeID, now)
		}
		if t.inventory != nil {
			t.inventory.MarkStarting(runtimeID, now)
		}
		err := t.Transport.Run(ctx)
		stoppedAt := time.Now()
		if err == nil || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			if t.manager != nil {
				t.manager.MarkRuntimeStopped(runtimeID, stoppedAt, nil)
			}
			if t.inventory != nil {
				t.inventory.MarkStopped(runtimeID, stoppedAt, nil)
			}
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			return nil
		}
		if !t.restartPolicy.ShouldRetry(restartCount) {
			if t.manager != nil {
				t.manager.MarkRuntimeStopped(runtimeID, stoppedAt, err)
			}
			if t.inventory != nil {
				t.inventory.MarkStopped(runtimeID, stoppedAt, err)
			}
			return err
		}

		backoff := t.restartPolicy.Backoff(restartCount)
		nextRetryAt := stoppedAt.Add(backoff)
		if t.manager != nil {
			t.manager.MarkRuntimeBackingOff(runtimeID, stoppedAt, nextRetryAt, err)
		}
		if t.inventory != nil {
			t.inventory.MarkBackingOff(runtimeID, stoppedAt, nextRetryAt, err)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if t.manager != nil {
				t.manager.MarkRuntimeStopped(runtimeID, time.Now(), nil)
			}
			if t.inventory != nil {
				t.inventory.MarkStopped(runtimeID, time.Now(), nil)
			}
			return nil
		case <-timer.C:
			restartCount++
		}
	}
}

type trackedTransport struct {
	channelcore.Transport
	inventory *TransportInventory
}

func (t trackedTransport) Name() string {
	if t.Transport == nil {
		return ""
	}
	return t.Transport.Name()
}

func (t trackedTransport) Run(ctx context.Context) error {
	if t.Transport == nil {
		return nil
	}
	name := t.Transport.Name()
	now := time.Now()
	if t.inventory != nil {
		t.inventory.MarkStarting(name, now)
	}
	err := t.Transport.Run(ctx)
	stoppedAt := time.Now()
	if t.inventory != nil {
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			t.inventory.MarkStopped(name, stoppedAt, err)
		} else {
			t.inventory.MarkStopped(name, stoppedAt, nil)
		}
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return nil
	}
	return err
}

type httpServerTransport struct {
	name    string
	address string
	handler http.Handler
	logger  *slog.Logger
}

func (t *httpServerTransport) Name() string {
	return t.name
}

func (t *httpServerTransport) Run(ctx context.Context) error {
	if t.logger != nil {
		t.logger.Info("gateway: starting webhook server", "transport", t.name, "address", t.address)
	}

	server := &http.Server{
		Addr:    t.address,
		Handler: t.handler,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if t.logger != nil {
		t.logger.Info("gateway: webhook server exited", "transport", t.name)
	}
	return nil
}

func buildHTTPServerTransports(
	routes []channelcore.HTTPRoute,
	logger *slog.Logger,
) ([]channelcore.Transport, error) {
	if len(routes) == 0 {
		return nil, nil
	}

	byAddress := make(map[string][]channelcore.HTTPRoute)
	addresses := make([]string, 0)
	for _, route := range routes {
		address := strings.TrimSpace(route.Address)
		if address == "" {
			return nil, fmt.Errorf("gateway: missing webhook address for route %s", route.Name)
		}
		if _, ok := byAddress[address]; !ok {
			addresses = append(addresses, address)
		}
		byAddress[address] = append(byAddress[address], route)
	}
	slices.Sort(addresses)

	transports := make([]channelcore.Transport, 0, len(addresses))
	for _, address := range addresses {
		mux := http.NewServeMux()
		paths := make(map[string]string)
		for _, route := range byAddress[address] {
			path := strings.TrimSpace(route.Path)
			if path == "" {
				return nil, fmt.Errorf("gateway: missing webhook path for route %s", route.Name)
			}
			if existing, ok := paths[path]; ok {
				return nil, fmt.Errorf(
					"gateway: duplicate webhook path %q for routes %s and %s on %s",
					path,
					existing,
					route.Name,
					address,
				)
			}
			mux.Handle(path, route.Handler)
			paths[path] = route.Name
		}
		registerHealthRoutes(mux)

		transports = append(transports, &httpServerTransport{
			name:    fmt.Sprintf("webhook[%s]", address),
			address: address,
			handler: mux,
			logger:  logger,
		})
	}
	return transports, nil
}

func buildWorkerTransports(app *runtime.App, logger *slog.Logger) []channelcore.Transport {
	if app == nil {
		return nil
	}

	transports := make([]channelcore.Transport, 0, 2)
	if worker := runtime.NewMemoryJobWorker(
		app.Repos,
		app.Memory,
		workspacectx.NewLoader(app.Config.Workspace.Root),
		logger,
	); worker.Enabled() {
		transports = append(transports, worker)
	}
	if worker := runtime.NewMemoryCompactionWorker(
		app.Repos,
		workspacectx.NewLoader(app.Config.Workspace.Root),
		app.Config.Memory.AutoPromote,
		logger,
	); worker.Enabled() {
		transports = append(transports, worker)
	}
	return transports
}

func runTransports(ctx context.Context, transports []channelcore.Transport) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(transports))
	doneCh := make(chan struct{})
	var wg sync.WaitGroup

	for _, transport := range transports {
		if transport == nil {
			continue
		}
		wg.Add(1)
		go func(transport channelcore.Transport) {
			defer wg.Done()
			if err := transport.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- fmt.Errorf("%s: %w", transport.Name(), err)
				cancel()
			}
		}(transport)
	}

	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case err := <-errCh:
		<-doneCh
		return err
	case <-doneCh:
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	case <-ctx.Done():
		<-doneCh
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}
}
