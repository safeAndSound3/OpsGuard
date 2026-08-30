package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"monitor-platform/internal/config"
	"monitor-platform/internal/router"
)

func main() {
	cfg := config.Load()
	mux, err := router.SetupRoutes(cfg)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       15 * time.Second,
	}

	log.Printf("server starting on http://%s:%s", cfg.Host, cfg.Port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
