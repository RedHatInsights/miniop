package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/prometheus/client_golang/api"
	prom "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func upgradeLoop() {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	promclient, err := api.NewClient(prom.Config{
		Address: "http://prometheus.mnm.svc:9090",
	})

	if err != nil {
		panic(err.Error())
	}

	promapi := promv1.NewAPI(promclient)

	for {
		pods, err := clientset.CoreV1().Pods("platform-prod").List(metav1.ListOptions{LabelSelector: "deploymentconfig=buck-it"})
		if err != nil {
			panic(err.Error())
		}

		fmt.Printf("There are %d pods in with the selector\n", len(pods.Items))

		r := promv1.Range{
			Start: time.Now().Add(-15 * time.Minute),
			End:   time.Now(),
			Step:  time.Minute,
		}

		result, warnings, err := promapi.QueryRange(context.TODO(), "sum(rate(buckit_count_total[5m]) == 0) by (kubernetes_pod_name)", r)
		if err != nil {
			panic(err.Error())
		}

		if len(warnings) > 0 {
			fmt.Printf("Warnings: %v\n", warnings)
		}

		fmt.Printf("Result:\n%v\n", result)

		time.Sleep(5 * time.Second)
	}
}

func main() {

	http.HandleFunc("/alert", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		defer r.Body.Close()
		alert, err := ioutil.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("failed to read post body: %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		fmt.Printf("Received an alert: %v\n", alert)
		w.WriteHeader(http.StatusOK)
	})

	var srv http.Server

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

	if err := srv.ListenAndServe(":8080"); err != http.ErrServerClosed {
		fmt.Printf("HTTP Server Failed to start: %v\n", err)
	}

	<-idleConnsClosed
}
