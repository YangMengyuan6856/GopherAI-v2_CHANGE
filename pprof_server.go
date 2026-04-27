package main

import (
	"GopherAI/config"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"time"
)

func StartPprofServer(conf *config.Config) error {
	if !conf.PprofConfig.Enabled {
		return nil
	}

	addr := fmt.Sprintf("%s:%d", conf.PprofConfig.Host, conf.PprofConfig.Port)
	mux := http.NewServeMux()
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	mux.Handle("/debug/pprof/cmdline", http.DefaultServeMux)
	mux.Handle("/debug/pprof/profile", http.DefaultServeMux)
	mux.Handle("/debug/pprof/symbol", http.DefaultServeMux)
	mux.Handle("/debug/pprof/trace", http.DefaultServeMux)
	mux.Handle("/debug/pprof/allocs", http.DefaultServeMux)
	mux.Handle("/debug/pprof/block", http.DefaultServeMux)
	mux.Handle("/debug/pprof/goroutine", http.DefaultServeMux)
	mux.Handle("/debug/pprof/heap", http.DefaultServeMux)
	mux.Handle("/debug/pprof/mutex", http.DefaultServeMux)
	mux.Handle("/debug/pprof/threadcreate", http.DefaultServeMux)

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  time.Duration(conf.PprofConfig.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(conf.PprofConfig.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		log.Printf("pprof server enabled on http://%s/debug/pprof/ (for profiling only)", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("pprof server failed: %v", err)
		}
	}()

	return nil
}
