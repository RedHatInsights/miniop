package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/redhatinsights/miniop/deployment"
	"github.com/redhatinsights/miniop/kill"
	l "github.com/redhatinsights/miniop/logger"

	"github.com/redhatinsights/miniop/pod"
	"go.uber.org/zap"
	"k8s.io/klog"
)

func init() {
	l.InitLogger()
}

func main() {

	klog.InitFlags(nil)
	flag.Parse()

	klog.V(9).Info("klog initialized with verbosity 9")

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

	go pod.NewWorker().Start()
	go deployment.NewDeploymentWorker().Start()

	l.Log.Info("starting web server")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		l.Log.Panic("HTTP server failed to start", zap.Error(err))
	}

	<-idleConnsClosed
}
