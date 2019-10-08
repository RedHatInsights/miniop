package main

import (
	"fmt"

	appsv1 "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	"github.com/redhatinsights/miniop/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getCanaryDeployments() {
	client, err := appsv1.NewForConfig(client.GetConfig())
	if err != nil {
		panic(err.Error())
	}
	dcs, err := client.DeploymentConfigs("").List(metav1.ListOptions{
		LabelSelector: "canary=true",
	})
	if err != nil {
		fmt.Printf("failed to fetch deploymentconfigs: %v\n", err)
		return
	}
	if len(dcs.Items) == 0 {
		fmt.Printf("0 deployment configs to be managed")
		return
	}
	for _, dc := range dcs.Items {
		image, ok := dc.Labels["canary-image"]
		if !ok {
			fmt.Printf("dc %s does not have an image defined. skipping...\n", dc.Name)
			continue
		}
		for _, container := range dc.Spec.Template.Spec.Containers {
			fmt.Printf("comparing %s to %s\n", container.Image, image)
		}
	}
}
