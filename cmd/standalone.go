package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"
)

const guiHelperName = "renart-gui"

// Standalone runs Renart as a desktop application. It boots the exact same
// HTTP server as `renart web` on a loopback port and spawns the renart-gui
// helper binary, which opens a native window pointed at it.
//
// The webview lives in a separate helper on purpose: the GUI toolkit links
// platform webview libraries at load time (webkit2gtk/gtk3 on Linux), so
// compiling it into this binary would prevent the whole CLI - including
// `renart web` - from starting on machines without those libraries. With the
// helper, a machine without those libraries can still use the browser UI.
func Standalone() *cli.Command {
	return &cli.Command{
		Name:      "standalone",
		Usage:     "run Renart as a desktop app",
		ArgsUsage: "[workspace root]",
		Category:  categoryApp,
		Flags: append(serverFlags(),
			&cli.IntFlag{
				Name:  "port",
				Value: 0,
				Usage: "loopback port for the embedded server (0 picks a free port)",
			},
			&cli.StringFlag{
				Name:    "gui-binary",
				Usage:   "path to the renart-gui helper binary",
				Sources: cli.EnvVars("RENART_GUI_BINARY"),
			},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			// The root command only prints ExitCoder errors, so wrap the
			// result to make failures (e.g. missing helper) visible.
			if err := runStandalone(ctx, c); err != nil {
				return cli.Exit(err.Error(), 1)
			}
			return nil
		},
	}
}

func runStandalone(ctx context.Context, c *cli.Command) error {
	// As in `renart web`: shut down on the first signal so the deferred
	// cleanups (discovery file, scheduler) run.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	helperPath, helperErr := locateGUIHelper(c.String("gui-binary"))

	cfg, err := serverConfigFromCommand(c)
	if err != nil {
		return err
	}
	defer cleanupServerBootstrap(cfg)

	logger, err := newServerLogger()
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	defaultRuntime, err := newProjectRuntime(ctx, logger, cfg)
	if err != nil {
		return err
	}
	defer defaultRuntime.cleanup()

	manager, err := newProjectManager(ctx, logger, cfg, defaultRuntime)
	if err != nil {
		return err
	}
	defer manager.closeAll()

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.Int("port")))
	if err != nil {
		return fmt.Errorf("failed to listen on loopback: %w", err)
	}
	defer listener.Close()

	address := listener.Addr().String()
	appURL := "http://" + address + "/"
	sessionToken := newSessionToken()
	httpServer := newHTTPServer(ctx, address, buildRootRouter(manager, defaultRuntime, sessionToken))

	manager.EnableDiscovery("http://"+address, sessionToken)
	defer manager.DisableDiscovery()

	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	logger.Info("standalone server listening", zap.String("url", appURL))
	printRenartWelcome(c.Writer, appURL, "")
	stopShutdownObserver := startGracefulShutdown(ctx, stop, logger, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Warn("standalone HTTP server did not stop gracefully", zap.Error(err))
		}
	})
	defer stopShutdownObserver()
	if helperErr != nil {
		return waitForStandaloneWebFallback(
			ctx,
			os.Stdout,
			appURL,
			address,
			serveErr,
			helperErr,
			openBrowserWhenReachable,
		)
	}

	helper := exec.CommandContext(ctx, helperPath, "--url", appURL)
	helper.Stdout = os.Stdout
	helper.Stderr = os.Stderr
	if err := helper.Start(); err != nil {
		return waitForStandaloneWebFallback(
			ctx,
			os.Stdout,
			appURL,
			address,
			serveErr,
			fmt.Errorf("failed to start %s: %w", guiHelperName, err),
			openBrowserWhenReachable,
		)
	}

	helperDone := make(chan error, 1)
	go func() { helperDone <- helper.Wait() }()

	select {
	case err := <-helperDone:
		if err != nil && ctx.Err() == nil {
			return waitForStandaloneWebFallback(
				ctx,
				os.Stdout,
				appURL,
				address,
				serveErr,
				fmt.Errorf("%s exited with an error: %w", guiHelperName, err),
				openBrowserWhenReachable,
			)
		}
		return nil
	case err := <-serveErr:
		// Server died underneath the window; take the helper down too.
		_ = helper.Process.Kill()
		<-helperDone
		if err != nil {
			return fmt.Errorf("embedded server failed: %w", err)
		}
		return nil
	}
}

func waitForStandaloneWebFallback(
	ctx context.Context,
	out io.Writer,
	appURL string,
	address string,
	serveErr <-chan error,
	cause error,
	openBrowser func(context.Context, string, string),
) error {
	_, _ = fmt.Fprintf(
		out,
		"Renart GUI is unavailable (%v). Falling back to the web interface at %s\n",
		cause,
		appURL,
	)
	go openBrowser(ctx, appURL, address)

	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("embedded server failed: %w", err)
		}
		return nil
	}
}

// locateGUIHelper finds the renart-gui helper: explicit flag/env first, then
// next to the current executable, then on PATH.
func locateGUIHelper(explicit string) (string, error) {
	helperName := guiHelperName
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}

	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("gui binary %q not usable: %w", explicit, err)
		}
		return explicit, nil
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), helperName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	if found, err := exec.LookPath(helperName); err == nil {
		return found, nil
	}

	return "", fmt.Errorf(
		"the %s helper binary was not found next to renart or on PATH; build it with `make standalone-build` or point --gui-binary/RENART_GUI_BINARY at it",
		guiHelperName,
	)
}
