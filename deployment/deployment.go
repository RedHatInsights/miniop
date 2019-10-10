package deployment

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

func init() {
	l.InitLogger()
}

// NothingToDo is returned as an error if a deployment is up to date
type NothingToDo struct{}

func (e *NothingToDo) Error() string {
	return fmt.Sprintf("nothing to do")
}

func GetCanaryDeployments() {
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

		failedImage, ok := dc.Annotations["canary-fail"]
		if ok {
			l.Log.Debug("a canary deployment has failed for this deploymentconfig, clear the annotations and try again",
				zap.String("deploymentconfig", dc.GetName()), zap.String("failed", failedImage))
			return
		}

		podName, err := spawnCanary(dc)
		if err == nil {
			dc.Annotations["canary-pod"] = podName
			deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(&dc)
		} else if err, ok := err.(*NothingToDo); ok {
			l.Log.Debug("deploymentconfig appears to be up to date", zap.String("deploymentconfig", dc.GetName()))
		} else {
			l.Log.Error("failed to spawn canary", zap.Error(err))
		}
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

// findImage returns the index of the container with the given name
func findImage(name string, containers []apiv1.Container) (int, error) {
	for idx, container := range containers {
		if container.Name == name {
			return idx, nil
		}
	}
	return -1, fmt.Errorf("container by name %s was not found", name)
}

func spawnCanary(dc v1.DeploymentConfig) (string, error) {
	podTemplateSpec := dc.Spec.Template.DeepCopy()

	name, image, err := getNameAndImage(dc)
	if err != nil {
		return "", err
	}

	idx, err := findImage(name, podTemplateSpec.Spec.Containers)
	if err != nil {
		return "", err
	}
	if podTemplateSpec.Spec.Containers[idx].Image == image {
		return "", &NothingToDo{}
	}
	podTemplateSpec.Spec.Containers[idx].Image = image

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

	duration, ok := dc.Annotations["canary-duration"]
	if !ok {
		duration = "15m"
	}
	if om.Annotations == nil {
		om.Annotations = make(map[string]string)
	}
	om.Annotations["canary-duration"] = duration

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

func check(pod *apiv1.Pod) {
	canaryFor, ok := pod.Labels["canary-for"]
	if !ok {
		l.Log.Error("canary pod does not have a canary-for label", zap.String("pod", pod.GetName()))
		return
	}

	dc, err := deploymentsClient.DeploymentConfigs(client.GetNamespace()).Get(canaryFor, metav1.GetOptions{})
	if err != nil {
		l.Log.Error("failed to fetch deployment", zap.Error(err))
		return
	}

	name, image, err := getNameAndImage(*dc)
	if err != nil {
		l.Log.Error("failed to get canary details from dc", zap.Error(err))
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
			deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)

			l.Log.Info("canary image had container restarts, marking as failed",
				zap.String("deploymentconfig", canaryFor), zap.String("canary", status.Image))

			if err := deletePod(pod); err != nil {
				l.Log.Error("failed to delete stale canary pod", zap.Error(err))
				return
			}
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
		l.Log.Debug(fmt.Sprintf("canary pod %s for deployment %s is not old enough, letting it ripen...", pod.GetName(), canaryFor))
		return
	}

	l.Log.Info(fmt.Sprintf("canary pod %s for deployment %s is old enough, upgrading the deployment...", pod.GetName(), canaryFor))
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
