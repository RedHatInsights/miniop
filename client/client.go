package client

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var Config *rest.Config
var Clientset *kubernetes.Clientset

func init() {
	Config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	Clientset, err = kubernetes.NewForConfig(Config)
	if err != nil {
		panic(err.Error())
	}
}
