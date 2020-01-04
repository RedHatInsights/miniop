package kill

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/prometheus/alertmanager/notify/webhook"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redhatinsights/miniop/client"
	l "github.com/redhatinsights/miniop/logger"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	l.InitLogger()
}

var killCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "pod_killer_total",
	Help: "A count of pods killed per deployment",
}, []string{"deployment"})

func kill(pod string) (int, error) {
	p, err := client.Clientset.CoreV1().Pods(client.Namespace).Get(pod, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return http.StatusNotFound, err
	} else if _, isStatus := err.(*errors.StatusError); isStatus {
		return http.StatusInternalServerError, err
	} else if err != nil {
		return http.StatusInternalServerError, err
	}

	p.Annotations["killed-by"] = "pod-killer"
	p, err = client.Clientset.CoreV1().Pods(client.Namespace).Update(p)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	err = client.Clientset.CoreV1().Pods(client.Namespace).Delete(p.GetName(), &metav1.DeleteOptions{})
	if err != nil {
		return http.StatusInternalServerError, err
	}
	killCounter.With(prometheus.Labels{"deployment": p.Labels["app"]}).Inc()
	return http.StatusOK, nil
}

func Handler(w http.ResponseWriter, r *http.Request) {
	webhookBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		l.Log.Error("failed to read post body", zap.Error(err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	var message webhook.Message
	err = json.Unmarshal(webhookBody, &message)
	if err != nil {
		l.Log.Error("failed to unmarshal json", zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	podname := message.CommonLabels["kubernetes_pod_name"]

	l.Log.Info(fmt.Sprintf("got a request to kill %s", podname), zap.String("pod", podname), zap.Reflect("message", message))

	code, err := kill(podname)
	if err != nil {
		l.Log.Error(fmt.Sprintf("failed to kill pod %s", podname), zap.Error(err))
	}
	w.WriteHeader(code)
}
