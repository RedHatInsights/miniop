package main

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	for {
		pods, err := clientset.CoreV1().Pods("platform-prod").List(metav1.ListOptions{LabelSelector: "deploymentconfig=buck-it"})
		if err != nil {
			panic(err.Error())
		}

		fmt.Printf("There are %d pods in with the selector\n", len(pods.Items))

		time.Sleep(5 * time.Second)
	}
}
