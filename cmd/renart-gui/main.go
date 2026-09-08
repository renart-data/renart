//go:build standalone

// renart-gui is the desktop window helper for `renart standalone`.
//
// It is a separate binary because the webview toolkit links platform
// libraries (webkit2gtk/gtk3 on Linux) at load time: keeping it out of the
// main renart binary keeps the CLI runnable on machines without a GUI
// stack. The standalone tag only exists so plain `go build ./...` does not
// require webview build dependencies; build the helper with
// `make standalone-build` or
// `go build -tags standalone,desktop,production ./cmd/renart-gui`
// (desktop and production are required by Wails at runtime).
//
// The helper deliberately contains no application logic: it receives the
// URL of the already-running loopback server and opens a native window on
// it. Serving the app over a real loopback listener (instead of mounting it
// on the Wails asset server) keeps SSE streaming and all other HTTP
// semantics identical to `renart web`.
package main

import (
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"renart/web"
)

func main() {
	appURL := flag.String("url", "", "URL of the running Renart server (required)")
	title := flag.String("title", "Renart", "window title")
	width := flag.Int("width", 1440, "initial window width")
	height := flag.Int("height", 900, "initial window height")
	flag.Parse()

	if err := run(*appURL, *title, *width, *height); err != nil {
		fmt.Fprintf(os.Stderr, "renart-gui: %v\n", err)
		os.Exit(1)
	}
}

func run(appURL, title string, width, height int) error {
	parsed, err := url.Parse(appURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("--url must be a valid absolute URL, got %q", appURL)
	}

	return wails.Run(&options.App{
		Title:     title,
		Width:     width,
		Height:    height,
		MinWidth:  900,
		MinHeight: 600,
		Linux: &linux.Options{
			Icon:        web.DesktopIcon(),
			ProgramName: "renart",
			// Preserve Wails' nil-Linux-options default when adding branding.
			WebviewGpuPolicy: linux.WebviewGpuPolicyNever,
		},
		AssetServer: &assetserver.Options{
			Handler: loaderHandler(parsed.String()),
		},
	})
}

// loaderHandler serves a minimal page that immediately navigates the webview
// to the loopback server, where the full app (including SSE) is served.
func loaderHandler(appURL string) http.Handler {
	page := template.Must(template.New("loader").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="0;url={{.URL}}">
<title>Renart</title>
</head>
<body style="background:#0a0a0a">
<script>window.location.replace({{.URL}});</script>
</body>
</html>
`))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, struct{ URL string }{URL: appURL})
	})
}
