package deployment

import (
	"fmt"

	v1 "github.com/openshift/api/apps/v1"
	appsv1 "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	"github.com/redhatinsights/miniop/client"
	ctl "github.com/redhatinsights/miniop/controller"
	l "github.com/redhatinsights/miniop/logger"
	"go.uber.org/zap"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

var deploymentsClient = appsv1.NewForConfigOrDie(client.GetConfig())
var clientset = client.GetClientset()

func init() {
	l.InitLogger()
}

func worker(c *ctl.Controller, key string) error {
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
		checkDeploymentConfig(obj.(*v1.DeploymentConfig))
	}
	return nil
}

// Start executes the watch loop
func Start() {

	dcListerWatcher := cache.NewFilteredListWatchFromClient(
		deploymentsClient.RESTClient(),
		"deploymentconfigs",
		client.GetNamespace(),
		func(opts *metav1.ListOptions) {
			opts.LabelSelector = "canary=true"
		},
	)

	ctl.Start(dcListerWatcher, &v1.DeploymentConfig{}, worker)
}

// NothingToDo is returned as an error if a deployment is up to date
type NothingToDo struct{}

func (e *NothingToDo) Error() string {
	return fmt.Sprintf("nothing to do")
}

func checkDeploymentConfig(dc *v1.DeploymentConfig) {
	_, ok := dc.Annotations["canary-pod"]
	if ok {
		l.Log.Debug(fmt.Sprintf("a canary pod for %s already exists", dc.Name), zap.String("deploymentconfig", dc.Name))
		return
	}

	failedImage, ok := dc.Annotations["canary-fail"]
	if ok {
		l.Log.Debug("a canary deployment has failed for this deploymentconfig, clear the annotations and try again",
			zap.String("deploymentconfig", dc.GetName()), zap.String("failed", failedImage))
		return
	}

	podName, err := spawnCanary(*dc)
	if err == nil {
		dc.Annotations["canary-pod"] = podName
		deploymentsClient.DeploymentConfigs(client.GetNamespace()).Update(dc)
	} else if err, ok := err.(*NothingToDo); ok {
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

	l.Log.Info("creating canary pod", zap.String("deploymentconfig", dc.GetName()))
	l.Log.Debug("pod definition", zap.Reflect("pod", podDef))

	pod, err := clientset.CoreV1().Pods(client.GetNamespace()).Create(podDef)
	if err != nil {
		return "", fmt.Errorf("Failed to create pod: %v", err)
	}

	return pod.Name, nil
}
