package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redhatinsights/miniop/deployment"
	"github.com/redhatinsights/miniop/kill"
	l "github.com/redhatinsights/miniop/logger"
	"github.com/redhatinsights/miniop/pod"
	"go.uber.org/zap"
)

func init() {
	l.InitLogger()
}

func main() {

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Post("/kill", kill.Handler)
	r.Handle("/metrics", promhttp.Handler())

	srv := http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		if err := srv.Shutdown(context.Background()); err != nil {
			l.Log.Error("HTTP Server Shutdown Error", zap.Error(err))
		}
		close(idleConnsClosed)
	}()

	go func(done chan struct{}) {
		for {
			select {
			case <-done:
				return
			default:
				pod.MonitorCanaries()
				time.Sleep(1 * time.Minute)
			}
		}
	}(idleConnsClosed)

	go deployment.Loop()

	l.Log.Info("starting web server")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		l.Log.Panic("HTTP server failed to start", zap.Error(err))
	}

	<-idleConnsClosed
}
