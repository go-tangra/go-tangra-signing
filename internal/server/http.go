package server

import (
	"io/fs"
	"net/http"
	"os"

	kratosHttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-signing/cmd/server/assets"
	"github.com/go-tangra/go-tangra-signing/internal/data"
	"github.com/go-tangra/go-tangra-signing/internal/service"
	appViewer "github.com/go-tangra/go-tangra-signing/pkg/viewer"
)

// NewHTTPServer creates an HTTP server for serving frontend assets and PDF proxy.
func NewHTTPServer(ctx *bootstrap.Context, storage *data.StorageClient, templateRepo *data.TemplateRepo) *kratosHttp.Server {
	l := ctx.NewLoggerHelper("signing/http")

	addr := os.Getenv("SIGNING_HTTP_ADDR")
	if addr == "" {
		addr = "0.0.0.0:10401"
	}

	srv := kratosHttp.NewServer(kratosHttp.Address(addr))

	route := srv.Route("/")
	route.GET("/health", func(ctx kratosHttp.Context) error {
		return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// PDF proxy endpoint — streams PDF from RustFS to browser
	route.GET("/api/v1/signing/templates/pdf", func(ctx kratosHttp.Context) error {
		key := ctx.Request().URL.Query().Get("key")
		if key == "" {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "missing key"})
		}

		content, err := storage.Download(ctx.Request().Context(), key)
		if err != nil {
			l.Errorf("failed to download PDF: %v", err)
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "PDF not found"})
		}

		ctx.Response().Header().Set("Content-Type", "application/pdf")
		ctx.Response().Header().Set("Cache-Control", "private, max-age=3600")
		ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")
		_, writeErr := ctx.Response().Write(content)
		return writeErr
	})

	// Detect fields endpoint — analyzes PDF with go-pdfplumber to find placeholders
	route.GET("/api/v1/signing/templates/detect-fields", func(ctx kratosHttp.Context) error {
		ctx.Response().Header().Set("Access-Control-Allow-Origin", "*")

		templateID := ctx.Request().URL.Query().Get("id")
		if templateID == "" {
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "id query parameter is required"})
		}

		// Look up template to get the file key (system viewer bypasses privacy policy)
		reqCtx := appViewer.NewSystemViewerContext(ctx.Request().Context())
		tpl, err := templateRepo.GetByID(reqCtx, templateID)
		if err != nil || tpl == nil {
			return ctx.JSON(http.StatusNotFound, map[string]string{"error": "template not found"})
		}

		// Download the PDF from storage
		pdfContent, err := storage.Download(ctx.Request().Context(), tpl.FileKey)
		if err != nil {
			l.Errorf("failed to download PDF for detection: %v", err)
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to download PDF"})
		}

		// Analyze the PDF for placeholder fields
		result, err := service.DetectPlaceholders(pdfContent)
		if err != nil {
			l.Errorf("failed to detect fields: %v", err)
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to analyze PDF"})
		}

		return ctx.JSON(http.StatusOK, result)
	})

	// Serve frontend static assets — must be registered last (catch-all)
	fsys, err := fs.Sub(assets.FrontendDist, "frontend-dist")
	if err == nil {
		fileServer := http.FileServer(http.FS(fsys))
		srv.HandlePrefix("/", fileServer)
		l.Infof("Serving embedded frontend assets")
	} else {
		l.Warnf("Failed to load embedded frontend assets: %v", err)
	}

	l.Infof("HTTP server listening on %s", addr)
	return srv
}
