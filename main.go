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
)

func main() {

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Post("/kill", killHandler)

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
		fmt.Println("Starting deployment scanner...")
		for {
			select {
			case <-done:
				return
			default:
				getCanaryDeployments()
				time.Sleep(10 * time.Second)
			}
		}
	}(idleConnsClosed)

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Printf("HTTP Server Failed to start: %v\n", err)
		panic(err.Error())
	}

	<-idleConnsClosed
}
