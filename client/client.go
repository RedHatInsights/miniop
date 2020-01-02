package client

import (
	"io/ioutil"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var Config *rest.Config
var Clientset *kubernetes.Clientset
var Namespace string

func init() {
	Config = getConfig()
	Clientset = getClientset()
	Namespace = getNamespace()
}

func getConfig() *rest.Config {
	Config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	return Config
}

func getClientset() *kubernetes.Clientset {
	Clientset, err := kubernetes.NewForConfig(getConfig())
	if err != nil {
		panic(err.Error())
	}
	return Clientset
}

func getNamespace() string {
	content, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		panic(err.Error())
	}
	return string(content)
}
