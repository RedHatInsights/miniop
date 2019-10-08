package client

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var Config *rest.Config
var Clientset *kubernetes.Clientset

func GetConfig() *rest.Config {
	if Config != nil {
		return Config
	}
	Config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	return Config
}

func GetClientset() *kubernetes.Clientset {
	if Clientset != nil {
		return Clientset
	}
	Clientset, err := kubernetes.NewForConfig(Config)
	if err != nil {
		panic(err.Error())
	}
	return Clientset
}
