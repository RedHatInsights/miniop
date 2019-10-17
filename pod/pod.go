package pod

import (
	"fmt"
	"time"

	v1 "github.com/openshift/api/apps/v1"
	appsv1 "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	"github.com/redhatinsights/miniop/client"
	ctl "github.com/redhatinsights/miniop/controller"
	l "github.com/redhatinsights/miniop/logger"
	"go.uber.org/zap"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

func init() {
	l.InitLogger()
}

type PodWorker struct {
	deploymentsClient *appsv1.AppsV1Client
	clientset         *kubernetes.Clientset
}

func NewWorker() *PodWorker {
	return &PodWorker{
		deploymentsClient: appsv1.NewForConfigOrDie(client.GetConfig()),
		clientset:         client.GetClientset(),
	}
}

func (p *PodWorker) Work(obj interface{}) error {
	pod, ok := obj.(*apiv1.Pod)
	if !ok {
		return fmt.Errorf("object type was unexpected")
	}
	p.check(pod)
	return nil
}

func (p *PodWorker) Start() {

	podListerWatcher := cache.NewFilteredListWatchFromClient(
		p.clientset.RESTClient(),
		"pods",
		client.GetNamespace(),
		func(opts *metav1.ListOptions) {
			opts.LabelSelector = "canary=true"
		},
	)

	ctl.Start(podListerWatcher, &apiv1.Pod{}, p, 60*time.Second)
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

func (p *PodWorker) check(pod *apiv1.Pod) {
	canaryFor, ok := pod.Labels["canary-for"]
	if !ok {
		l.Log.Debug("canary pod does not have a canary-for label", zap.String("pod", pod.GetName()))
		return
	}

	dc, err := p.deploymentsClient.DeploymentConfigs(client.GetNamespace()).Get(canaryFor, metav1.GetOptions{})
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
			if err := p.deletePod(pod); err != nil {
				l.Log.Error("failed to delete stale canary pod", zap.Error(err))
				return
			}
			l.Log.Info("canary image didn't match desired image from dc, deleted",
				zap.String("deploymentconfig", canaryFor), zap.String("desired", image), zap.String("canary", status.Image))
		}

		if status.RestartCount > 0 {
			dc.Annotations["canary-fail"] = status.Image
			delete(dc.Annotations, "canary-pod")
			p.deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)

			l.Log.Info("canary image had container restarts, marking as failed",
				zap.String("deploymentconfig", canaryFor), zap.String("canary", status.Image))

			if err := p.deletePod(pod); err != nil {
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
	p.upgrade(pod, canaryFor)
}

func (p *PodWorker) upgrade(pod *apiv1.Pod, canaryFor string) {
	dc, err := p.deploymentsClient.DeploymentConfigs(client.GetNamespace()).Get(canaryFor, metav1.GetOptions{})
	if err != nil {
		l.Log.Error("failed to fetch deployment", zap.Error(err))
		return
	}
	if ok := updateContainer(dc); !ok {
		l.Log.Error("failed to update image in container specs")
		return
	}

	if err := p.deletePod(pod); err != nil {
		l.Log.Error("failed to delete pod, not updating deployment", zap.Error(err))
		return
	}

	delete(dc.Annotations, "canary-pod")
	p.deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)
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

func (p *PodWorker) deletePod(pod *apiv1.Pod) error {
	if err := p.clientset.CoreV1().Pods(client.GetNamespace()).Delete(pod.GetName(), &metav1.DeleteOptions{}); err != nil {
		return err
	}
	return nil
}
