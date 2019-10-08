package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"

	"github.com/prometheus/alertmanager/notify/webhook"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var clientset *kubernetes.Clientset

func init() {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
}

func kill(pod string) (int, error) {
	p, err := clientset.CoreV1().Pods("").Get(pod, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return http.StatusNotFound, err
	} else if _, isStatus := err.(*errors.StatusError); isStatus {
		return http.StatusInternalServerError, err
	} else if err != nil {
		return http.StatusInternalServerError, err
	}

	err = clientset.CoreV1().Pods(p.Namespace).Delete(p.GetName(), &metav1.DeleteOptions{})
	if err != nil {
		return http.StatusInternalServerError, err
	}

	return http.StatusOK, nil
}

func main() {

	http.HandleFunc("/kill", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		defer r.Body.Close()
		webhookBody, err := ioutil.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("failed to read post body: %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var message webhook.Message
		err = json.Unmarshal(webhookBody, &message)
		if err != nil {
			fmt.Printf("failed to unmarshal json: %v\n", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		fmt.Printf("Got a request to kill %s", message.CommonLabels["kubernetes_pod_name"])

		code, err := kill(message.CommonLabels["kubernetes_pod_name"])
		if err != nil {
			fmt.Printf("failed to kill pod: %v", err)
		}

		w.WriteHeader(code)
	})

	srv := http.Server{
		Addr: ":8080",
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

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Printf("HTTP Server Failed to start: %v\n", err)
		panic(err.Error())
	}

	<-idleConnsClosed
}
