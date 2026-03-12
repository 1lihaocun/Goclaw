package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"

	channelcore "goclaw/internal/channels"
	workspacectx "goclaw/internal/workspace"
)

func (a *App) HTTPHandler() (http.Handler, error) {
	buildParams := a.NewBuildRuntimeParams(slog.Default())
	routes, err := a.Channels.HTTPRoutes(buildParams)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, errors.New("runtime: no channel webhook routes are enabled")
	}

	transports, err := buildHTTPServerTransports(routes, slog.Default())
	if err != nil {
		return nil, err
	}
	if len(transports) != 1 {
		return nil, fmt.Errorf("runtime: HTTP handler requires exactly one webhook listener, got %d", len(transports))
	}

	serverTransport, ok := transports[0].(*httpServerTransport)
	if !ok {
		return nil, errors.New("runtime: unexpected webhook transport type")
	}
	return serverTransport.handler, nil
}

func (a *App) Serve(ctx context.Context) error {
	buildParams := a.NewBuildRuntimeParams(slog.Default())

	routes, err := a.Channels.HTTPRoutes(buildParams)
	if err != nil {
		return err
	}
	httpTransports, err := buildHTTPServerTransports(routes, buildParams.Logger)
	if err != nil {
		return err
	}
	backgroundTransports, err := a.Channels.Transports(buildParams)
	if err != nil {
		return err
	}

	transports := make([]channelcore.Transport, 0, len(httpTransports)+len(backgroundTransports))
	transports = append(transports, httpTransports...)
	transports = append(transports, backgroundTransports...)
	if worker := NewMemoryJobWorker(
		a.Repos,
		a.Memory,
		workspacectx.NewLoader(a.Config.Workspace.Root),
		buildParams.Logger,
	); worker.Enabled() {
		transports = append(transports, worker)
	}
	if worker := NewMemoryCompactionWorker(
		a.Repos,
		workspacectx.NewLoader(a.Config.Workspace.Root),
		a.Config.Memory.AutoPromote,
		buildParams.Logger,
	); worker.Enabled() {
		transports = append(transports, worker)
	}
	if len(transports) == 0 {
		return errors.New("runtime: no enabled channel transports")
	}

	return runTransports(ctx, transports)
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
		t.logger.Info("channel transport: starting webhook server", "transport", t.name, "address", t.address)
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
		t.logger.Info("channel transport: webhook server exited", "transport", t.name)
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
			return nil, fmt.Errorf("runtime: missing webhook address for route %s", route.Name)
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
				return nil, fmt.Errorf("runtime: missing webhook path for route %s", route.Name)
			}
			if existing, ok := paths[path]; ok {
				return nil, fmt.Errorf(
					"runtime: duplicate webhook path %q for routes %s and %s on %s",
					path,
					existing,
					route.Name,
					address,
				)
			}
			mux.Handle(path, route.Handler)
			paths[path] = route.Name
		}
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})

		transports = append(transports, &httpServerTransport{
			name:    fmt.Sprintf("webhook[%s]", address),
			address: address,
			handler: mux,
			logger:  logger,
		})
	}
	return transports, nil
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
