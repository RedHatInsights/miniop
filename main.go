package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redhatinsights/miniop/kill"
)

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
			fmt.Printf("HTTP Server Shutdown Error: %v\n", err)
		}
		close(idleConnsClosed)
	}()

	// start deployment scanner
	go func(done chan struct{}) {
		for {
			select {
			case <-done:
				return
			default:
				getCanaryDeployments()
				time.Sleep(2 * time.Minute)
			}
		}
	}(idleConnsClosed)

	go func(done chan struct{}) {
		for {
			select {
			case <-done:
				return
			default:
				upgradeDeployments()
				time.Sleep(2 * time.Minute)
			}
		}
	}(idleConnsClosed)

	fmt.Println("Starting web server")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Printf("HTTP Server Failed to start: %v\n", err)
		panic(err.Error())
	}

	<-idleConnsClosed
}
