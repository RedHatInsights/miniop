package pod

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

var clientset = client.GetClientset()
var deploymentsClient = appsv1.NewForConfigOrDie(client.GetConfig())

func init() {
	l.InitLogger()
}

func MonitorCanaries() {
	pods, err := clientset.CoreV1().Pods(client.GetNamespace()).List(metav1.ListOptions{
		LabelSelector: "canary=true",
	})
	if err != nil {
		l.Log.Error("failed to select pods", zap.Error(err))
		return
	}
	for _, pod := range pods.Items {
		check(&pod)
	}
}

func getNameAndImage(dc v1.DeploymentConfig) (string, string, error) {
	var nameErr, imageErr error
	name, ok := dc.Annotations["canary-name"]
	if !ok {
		nameErr = fmt.Errorf("dc %s does not have an container name defined", dc.Name)
	}
	image, ok := dc.Annotations["canary-image"]
	if !ok {
		imageErr = fmt.Errorf("dc %s does not have an image defined", dc.Name)
	}
	if nameErr != nil || imageErr != nil {
		return "", "", fmt.Errorf("one or more details are missing: nameErr: %s, imageErr: %s", nameErr, imageErr)
	}
	return name, image, nil
}

func check(pod *apiv1.Pod) {
	canaryFor, ok := pod.Labels["canary-for"]
	if !ok {
		l.Log.Debug("canary pod does not have a canary-for label", zap.String("pod", pod.GetName()))
		return
	}

	dc, err := deploymentsClient.DeploymentConfigs(client.GetNamespace()).Get(canaryFor, metav1.GetOptions{})
	if err != nil {
		l.Log.Error("failed to fetch deployment", zap.Error(err))
		return
	}

	name, image, err := getNameAndImage(*dc)
	if err != nil {
		l.Log.Info("failed to get canary details from dc", zap.Error(err))
		return
	}

	for _, status := range pod.Status.ContainerStatuses {
		if status.Name != name {
			continue
		}

		if status.Image != image {
			// this canary is likely out of date
			if err := deletePod(pod); err != nil {
				l.Log.Error("failed to delete stale canary pod", zap.Error(err))
				return
			}
			l.Log.Info("canary image didn't match desired image from dc, deleted",
				zap.String("deploymentconfig", canaryFor), zap.String("desired", image), zap.String("canary", status.Image))
		}

		if status.RestartCount > 0 {
			dc.Annotations["canary-fail"] = status.Image
			delete(dc.Annotations, "canary-pod")
			deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)

			l.Log.Info("canary image had container restarts, marking as failed",
				zap.String("deploymentconfig", canaryFor), zap.String("canary", status.Image))

			if err := deletePod(pod); err != nil {
				l.Log.Error("failed to delete stale canary pod", zap.Error(err))
			}
			return
		}
	}

	durationString, ok := pod.Annotations["canary-duration"]
	if !ok {
		durationString = "15m"
	}

	duration, err := time.ParseDuration(durationString)
	if err != nil {
		duration = 15 * time.Minute
	}

	deadline := pod.GetCreationTimestamp().Add(duration)
	if !time.Now().After(deadline) {
		l.Log.Debug(fmt.Sprintf("canary pod %s for deployment %s is not old enough, letting it ripen...", pod.GetName(), canaryFor), zap.String("deploymentconfig", canaryFor))
		return
	}

	l.Log.Info(fmt.Sprintf("canary pod %s for deployment %s is old enough, upgrading the deployment...", pod.GetName(), canaryFor), zap.String("deploymentconfig", canaryFor))
	upgrade(pod, canaryFor)
}

func upgrade(pod *apiv1.Pod, canaryFor string) {
	dc, err := deploymentsClient.DeploymentConfigs(client.GetNamespace()).Get(canaryFor, metav1.GetOptions{})
	if err != nil {
		l.Log.Error("failed to fetch deployment", zap.Error(err))
		return
	}
	if ok := updateContainer(dc); !ok {
		l.Log.Error("failed to update image in container specs")
		return
	}

	if err := deletePod(pod); err != nil {
		l.Log.Error("failed to delete pod, not updating deployment", zap.Error(err))
		return
	}

	delete(dc.Annotations, "canary-pod")
	deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)
	l.Log.Info(fmt.Sprintf("canary for %s completed, upgrading", canaryFor), zap.String("deploymentconfig", canaryFor))
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

func deletePod(pod *apiv1.Pod) error {
	if err := clientset.CoreV1().Pods(client.GetNamespace()).Delete(pod.GetName(), &metav1.DeleteOptions{}); err != nil {
		return err
	}
	return nil
}