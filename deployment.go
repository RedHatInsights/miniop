package main

import (
	"fmt"
	"time"

	v1 "github.com/openshift/api/apps/v1"
	appsv1 "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	"github.com/redhatinsights/miniop/client"
	l "github.com/redhatinsights/miniop/logger"
	"go.uber.org/zap"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var deploymentsClient = appsv1.NewForConfigOrDie(client.GetConfig())
var clientset = client.GetClientset()

func getCanaryDeployments() {
	dcs, err := deploymentsClient.DeploymentConfigs(client.GetNamespace()).List(metav1.ListOptions{
		LabelSelector: "canary=true",
	})
	if err != nil {
		l.Log.Error("failed to fetch deploymentconfigs", zap.Error(err))
		return
	}
	if len(dcs.Items) == 0 {
		l.Log.Debug("0 deployment configs to be managed")
		return
	}

	for _, dc := range dcs.Items {
		_, ok := dc.Annotations["canary-pod"]
		if ok {
			l.Log.Debug(fmt.Sprintf("a canary pod for %s already exists", dc.Name), zap.String("deploymentconfig", dc.Name))
			continue
		}
		podName, err := spawnCanary(dc)
		if err != nil {
			l.Log.Error("failed to spawn canary", zap.Error(err))
			continue
		}
		dc.Annotations["canary-pod"] = podName
		deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(&dc)
	}
}

func spawnCanary(dc v1.DeploymentConfig) (string, error) {
	podTemplateSpec := dc.Spec.Template.DeepCopy()

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
		LabelSelector: fmt.Sprintf("canary=%s", dc.GetName()),
	})
	if err != nil {
		return "", fmt.Errorf("Failed to search for pods: %v", err)
	}

	if len(pods.Items) > 0 {
		return "", fmt.Errorf("A canary for this (%s) deployment already exists", dc.GetName())
	}

	l.Log.Debug("incoming dc", zap.Reflect("deploymentconfig", dc))

	om := podTemplateSpec.ObjectMeta

	delete(om.Labels, "deploymentconfig")
	om.Labels["canary"] = "true"
	om.Labels["canary-for"] = dc.GetName()
	om.SetGenerateName(fmt.Sprintf("%s-canary-", dc.GetName()))

	podDef := &apiv1.Pod{
		Spec:       podTemplateSpec.Spec,
		ObjectMeta: om,
	}

	l.Log.Debug("creating pod", zap.Reflect("pod", podDef))

	pod, err := clientset.CoreV1().Pods(client.GetNamespace()).Create(podDef)
	if err != nil {
		return "", fmt.Errorf("Failed to create pod: %v", err)
	}

	return pod.Name, nil
}

func upgradeDeployments() {
	pods, err := clientset.CoreV1().Pods(client.GetNamespace()).List(metav1.ListOptions{
		LabelSelector: "canary=true",
	})
	if err != nil {
		l.Log.Error("failed to select pods", zap.Error(err))
		return
	}
	for _, pod := range pods.Items {
		_, ok := pod.Labels["canary-for"]
		if !ok {
			continue
		}
		doUpgrade(&pod)
	}
}

func updateContainer(dc *v1.DeploymentConfig) bool {
	for idx, container := range dc.Spec.Template.Spec.Containers {
		if container.Name == dc.Annotations["canary-name"] {
			dc.Spec.Template.Spec.Containers[idx].Image = dc.Annotations["canary-image"]
			return true
		}
	}
	return false
}

func doUpgrade(pod *apiv1.Pod) {
	deadline := pod.GetCreationTimestamp().Add(15 * time.Minute)
	canaryFor := pod.Labels["canary-for"]
	if !time.Now().After(deadline) {

		l.Log.Debug(fmt.Sprintf("canary pod %s for deployment %s is not old enough, letting it ripen...", pod.GetName(), canaryFor))
		return
	}

	l.Log.Info(fmt.Sprintf("canary pod %s for deployment %s is old enough, upgrading the deployment...", pod.GetName(), canaryFor))
	dc, err := deploymentsClient.DeploymentConfigs(client.GetNamespace()).Get(canaryFor, metav1.GetOptions{})
	if err != nil {
		l.Log.Error("failed to fetch deployment", zap.Error(err))
		return
	}
	if ok := updateContainer(dc); !ok {
		l.Log.Error("failed to update image in container specs")
		return
	}

	if err := clientset.CoreV1().Pods(client.GetNamespace()).Delete(pod.GetName(), &metav1.DeleteOptions{}); err != nil {
		l.Log.Error("failed to delete pod, not updating deployment", zap.Error(err))
		return
	}

	delete(dc.Annotations, "canary-pod")
	deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)
}
