package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"monitor-platform/internal/config"
	"monitor-platform/internal/router"
	"monitor-platform/internal/tlsbootstrap"
)

func main() {
	cfg := config.Load()
	if closeLog, err := configureFileLogging(cfg.LogFile); err != nil {
		log.Printf("无法打开服务日志文件 %s：%v，继续输出到终端", cfg.LogFile, err)
	} else {
		defer closeLog()
	}
	mux, err := router.SetupRoutes(cfg)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       15 * time.Second,
	}

	if cfg.TLSEnabled {
		names := append([]string{}, cfg.TLSServerNames...)
		if cfg.Host != "" && cfg.Host != "0.0.0.0" && cfg.Host != "::" {
			names = append(names, cfg.Host)
		}
		if err := tlsbootstrap.Ensure(cfg.TLSCertFile, cfg.TLSKeyFile, names); err != nil {
			log.Fatal(err)
		}
		log.Printf("server starting on https://%s:%s", cfg.Host, cfg.Port)
		if err := server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
		return
	}
	log.Printf("server starting on http://%s:%s", cfg.Host, cfg.Port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func configureFileLogging(path string) (func(), error) {
	path = filepath.Clean(path)
	if path == "." || path == "" {
		return func() {}, fmt.Errorf("日志文件路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return func() {}, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return func() {}, err
	}
	log.SetOutput(io.MultiWriter(os.Stdout, file))
	return func() { _ = file.Close() }, nil
}
