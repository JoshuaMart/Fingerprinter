package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/JoshuaMart/fingerprinter/internal/config"
	"github.com/JoshuaMart/fingerprinter/internal/models"
	"github.com/JoshuaMart/fingerprinter/internal/scanner"
)

var version = "dev"

// Server is the HTTP server for the Fingerprinter Core API.
type Server struct {
	cfg     *config.Config
	scanner *scanner.Scanner
	router  *chi.Mux
}

// New creates a new Server with a fully initialized scanner.
func New(cfg *config.Config, scn *scanner.Scanner) *Server {
	s := &Server{cfg: cfg, scanner: scn}
	s.setupRouter()
	return s
}

// NewHealthOnly creates a Server that only exposes the /health endpoint.
func NewHealthOnly(cfg *config.Config) *Server {
	s := &Server{cfg: cfg}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/health", s.handleHealth)
	s.router = r
	return s
}

func (s *Server) setupRouter() {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(s.cfg.Server.ReadTimeout))

	r.Get("/health", s.handleHealth)
	r.Get("/detections", s.handleDetections)
	r.Post("/scan", s.handleScan)

	s.router = r
}

// Run starts the HTTP server and blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Server.Port)
	srv := &http.Server{
		Addr:        addr,
		Handler:     s.router,
		ReadTimeout: s.cfg.Server.ReadTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// Handler returns the HTTP handler (useful for testing).
func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version,
	})
}

type detectionInfo struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

func (s *Server) handleDetections(w http.ResponseWriter, _ *http.Request) {
	dets := s.scanner.Detectors()
	infos := make([]detectionInfo, len(dets))
	for i, d := range dets {
		infos[i] = detectionInfo{Name: d.Name(), Category: d.Category()}
	}
	writeJSON(w, http.StatusOK, map[string]any{"detections": infos})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req models.ScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}

	if req.Options == nil {
		req.Options = &models.ScanOptions{}
	}

	result, err := s.scanner.Scan(r.Context(), req)
	if err != nil {
		if r.Context().Err() != nil {
			writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "scan timed out"})
			return
		}
		slog.Error("scan failed", "url", req.URL, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "scan failed: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
