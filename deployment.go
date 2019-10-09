package main

import (
	"fmt"

	v1 "github.com/openshift/api/apps/v1"
	appsv1 "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	"github.com/redhatinsights/miniop/client"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getCanaryDeployments() {
	cl, err := appsv1.NewForConfig(client.GetConfig())
	if err != nil {
		panic(err.Error())
	}
	dcs, err := cl.DeploymentConfigs(client.GetNamespace()).List(metav1.ListOptions{
		LabelSelector: "canary=true",
	})
	if err != nil {
		fmt.Printf("failed to fetch deploymentconfigs: %v\n", err)
		return
	}
	if len(dcs.Items) == 0 {
		fmt.Printf("0 deployment configs to be managed\n")
		return
	}

	for _, dc := range dcs.Items {
		_, ok := dc.Annotations["canary-pod"]
		if ok {
			fmt.Printf("a canary pod for %s already exists\n", dc.Name)
			continue
		}
		podName, err := spawnCanary(dc)
		if err != nil {
			fmt.Printf("Failed to spawn canary: %v\n", err)
			continue
		}
		dc.Annotations["canary-pod"] = podName
		cl.DeploymentConfigs(client.GetNamespace()).Update(&dc)
	}
}

func spawnCanary(dc v1.DeploymentConfig) (string, error) {
	podTemplateSpec := dc.Spec.Template.DeepCopy()
	clientset := client.GetClientset()

	name, ok := dc.Annotations["canary-name"]
	if !ok {
		return "", fmt.Errorf("dc %s does not have an container name defined", dc.Name)
	}
	image, ok := dc.Annotations["canary-image"]
	if !ok {
		return "", fmt.Errorf("dc %s does not have an image defined", dc.Name)
	}

	for idx, container := range podTemplateSpec.Spec.Containers {
		if container.Name == name && container.Image != image {
			podTemplateSpec.Spec.Containers[idx].Image = image
			break
		}
		return "", fmt.Errorf("dc has no images to upgrade")
	}

	pods, err := clientset.CoreV1().Pods(client.GetNamespace()).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("canary=%s", podTemplateSpec.Name),
	})
	if err != nil {
		return "", fmt.Errorf("Failed to search for pods: %v", err)
	}

	if len(pods.Items) > 0 {
		return "", fmt.Errorf("A canary for this (%s) deployment already exists", podTemplateSpec.Name)
	}

	delete(podTemplateSpec.ObjectMeta.Labels, "deploymentconfig")
	podTemplateSpec.ObjectMeta.Labels["canary"] = podTemplateSpec.Name

	podDef := &apiv1.Pod{
		Spec:       podTemplateSpec.Spec,
		ObjectMeta: podTemplateSpec.ObjectMeta,
	}
	fmt.Printf("attempting to create this pod: %+v\n", podDef)

	pod, err := clientset.CoreV1().Pods(client.GetNamespace()).Create(podDef)
	if err != nil {
		return "", fmt.Errorf("Failed to create pod: %v", err)
	}

	return pod.Name, nil
}
