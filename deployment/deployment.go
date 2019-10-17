package deployment

import (
	"errors"
	"fmt"

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

type DeploymentWorker struct {
	deploymentsClient *appsv1.AppsV1Client
	clientset         *kubernetes.Clientset
}

func NewDeploymentWorker() *DeploymentWorker {
	return &DeploymentWorker{
		deploymentsClient: appsv1.NewForConfigOrDie(client.GetConfig()),
		clientset:         client.GetClientset(),
	}
}

func (d *DeploymentWorker) Work(c *ctl.Controller, key string) error {
	obj, exists, err := c.Indexer.GetByKey(key)
	if err != nil {
		l.Log.Error(fmt.Sprintf("Fetching object with key %s from store failed with %v", key, err), zap.Error(err))
		return err
	}

	if !exists {
		// Below we will warm up our cache with a Pod, so that we will see a delete for one pod
		l.Log.Debug(fmt.Sprintf("deploymentconfig %s does not exist anymore", key))
	} else {
		// Note that you also have to check the uid if you have a local controlled resource, which
		// is dependent on the actual instance, to detect that a Pod was recreated with the same name
		d.checkDeploymentConfig(obj.(*v1.DeploymentConfig))
	}
	return nil
}

// Start executes the watch loop
func (d *DeploymentWorker) Start() {

	dcListerWatcher := cache.NewFilteredListWatchFromClient(
		d.deploymentsClient.RESTClient(),
		"deploymentconfigs",
		client.GetNamespace(),
		func(opts *metav1.ListOptions) {
			opts.LabelSelector = "canary=true"
		},
	)

	ctl.Start(dcListerWatcher, &v1.DeploymentConfig{}, d, 0)
}

// NothingToDo is returned as an error if a deployment is up to date
var NothingToDo = errors.New("nothing to do")

func shouldSpawn(dc *v1.DeploymentConfig) ([]apiv1.Container, error) {
	_, ok := dc.Annotations["canary-pod"]
	if ok {
		l.Log.Debug(fmt.Sprintf("a canary pod for %s already exists", dc.Name), zap.String("deploymentconfig", dc.Name))
		return nil, NothingToDo
	}

	failedImage, ok := dc.Annotations["canary-fail"]
	if ok {
		l.Log.Debug("a canary deployment has failed for this deploymentconfig, clear the annotations and try again",
			zap.String("deploymentconfig", dc.GetName()), zap.String("failed", failedImage))
		return nil, NothingToDo
	}

	name, image, err := getNameAndImage(*dc)
	if err != nil {
		return nil, err
	}

	containers := dc.Spec.Template.Spec.Containers

	idx, err := findImage(name, containers)
	if err != nil {
		return nil, err
	}
	if containers[idx].Image == image {
		return nil, NothingToDo
	}

	newContainers := dc.Spec.Template.Spec.DeepCopy().Containers
	newContainers[idx].Image = image

	return newContainers, nil
}

func (d *DeploymentWorker) checkDeploymentConfig(dc *v1.DeploymentConfig) {

	containers, err := shouldSpawn(dc)
	if err != nil {
		return
	}

	podName, err := d.spawnCanary(*dc, containers)
	if err == nil {
		dc.Annotations["canary-pod"] = podName
		d.deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)
	} else if err == NothingToDo {
		l.Log.Debug("deploymentconfig appears to be up to date", zap.String("deploymentconfig", dc.GetName()))
	} else {
		l.Log.Error("failed to spawn canary", zap.Error(err))
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

func updateObjectMeta(objMeta *metav1.ObjectMeta, dc *v1.DeploymentConfig) {
	delete(objMeta.Labels, "deploymentconfig")
	objMeta.Labels["canary"] = "true"
	objMeta.Labels["canary-for"] = dc.GetName()

	duration, ok := dc.Annotations["canary-duration"]
	if !ok {
		duration = "15m"
	}
	if objMeta.Annotations == nil {
		objMeta.Annotations = make(map[string]string)
	}
	objMeta.Annotations["canary-duration"] = duration

	objMeta.SetGenerateName(fmt.Sprintf("%s-canary-", dc.GetName()))
}

func (d *DeploymentWorker) spawnCanary(dc v1.DeploymentConfig, containers []apiv1.Container) (string, error) {
	podTemplateSpec := dc.Spec.Template.DeepCopy()
	podTemplateSpec.Spec.Containers = containers

	pods, err := d.clientset.CoreV1().Pods(client.GetNamespace()).List(metav1.ListOptions{
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
	updateObjectMeta(&om, &dc)

	podDef := &apiv1.Pod{
		Spec:       podTemplateSpec.Spec,
		ObjectMeta: om,
	}

	l.Log.Info("creating canary pod", zap.String("deploymentconfig", dc.GetName()))
	l.Log.Debug("pod definition", zap.Reflect("pod", podDef))

	pod, err := d.clientset.CoreV1().Pods(client.GetNamespace()).Create(podDef)
	if err != nil {
		return "", fmt.Errorf("Failed to create pod: %v", err)
	}

	return pod.Name, nil
}
