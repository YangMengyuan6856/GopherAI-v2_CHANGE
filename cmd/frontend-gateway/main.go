package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	listenAddress := flag.String("listen", ":8080", "frontend listen address")
	backendAddress := flag.String("backend", "http://127.0.0.1:9090", "backend origin")
	distDirectory := flag.String("dist", "vue-frontend/dist", "built Vue distribution directory")
	flag.Parse()

	backendURL, err := url.Parse(strings.TrimSpace(*backendAddress))
	if err != nil || backendURL.Scheme == "" || backendURL.Host == "" {
		log.Fatalf("invalid backend origin: %q", *backendAddress)
	}
	handler, err := newGatewayHandler(*distDirectory, backendURL)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr: *listenAddress, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 75 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("frontend gateway shutdown: %v", err)
		}
	}()
	log.Printf("frontend gateway listening=%s backend=%s dist=%s", *listenAddress, backendURL.Redacted(), *distDirectory)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func newGatewayHandler(distDirectory string, backendURL *url.URL) (http.Handler, error) {
	root, err := filepath.Abs(strings.TrimSpace(distDirectory))
	if err != nil {
		return nil, fmt.Errorf("resolve frontend dist directory: %w", err)
	}
	indexPath := filepath.Join(root, "index.html")
	if info, err := os.Stat(indexPath); err != nil || info.IsDir() {
		return nil, fmt.Errorf("frontend dist index is unavailable: %s", indexPath)
	}
	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	proxy.FlushInterval = -1
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		if request.URL.Path == "/api" {
			request.URL.Path = "/api/v1"
		} else if strings.HasPrefix(request.URL.Path, "/api/") {
			request.URL.Path = "/api/v1/" + strings.TrimPrefix(request.URL.Path, "/api/")
		}
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"code":"FRONTEND_UPSTREAM_UNAVAILABLE","message":"后端服务暂时不可用"}`))
	}

	mux := http.NewServeMux()
	mux.Handle("/api", proxy)
	mux.Handle("/api/", proxy)
	mux.Handle("/health", proxy)
	mux.Handle("/health/", proxy)
	mux.Handle("/metrics", proxy)
	mux.Handle("/metrics/", proxy)
	mux.Handle("/", spaHandler(root, indexPath))
	return securityHeaders(mux), nil
}

func spaHandler(root string, indexPath string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		relative := strings.TrimPrefix(filepath.Clean("/"+request.URL.Path), string(filepath.Separator))
		candidate := filepath.Join(root, relative)
		if relative != "." && relative != "" {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				fileServer.ServeHTTP(writer, request)
				return
			}
		}
		http.ServeFile(writer, request, indexPath)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
		writer.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(writer, request)
	})
}
