package client

import (
	"io/ioutil"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var Config *rest.Config
var Clientset *kubernetes.Clientset
var Namespace string

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
	Clientset, err := kubernetes.NewForConfig(GetConfig())
	if err != nil {
		panic(err.Error())
	}
	return Clientset
}

func GetNamespace() string {
	if Namespace != "" {
		return Namespace
	}
	content, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	Namespace = string(content)
	return Namespace
}
