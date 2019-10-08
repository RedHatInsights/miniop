package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/prometheus/alertmanager/notify/webhook"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func killHandler(w http.ResponseWriter, r *http.Request) {
	webhookBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		fmt.Printf("failed to read post body: %v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	r.Body.Close()

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
}
