package deployment

import (
	"fmt"
	"testing"

	v1 "github.com/openshift/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var dc = &v1.DeploymentConfig{
	ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{
			"canary": "true",
		},
		Annotations: map[string]string{
			"canary-name":  "foo",
			"canary-image": "barv2",
		},
		Name: "testing",
	},
	Spec: v1.DeploymentConfigSpec{
		Template: &apiv1.PodTemplateSpec{
			Spec: apiv1.PodSpec{
				Containers: []apiv1.Container{
					apiv1.Container{
						Name:  "foo",
						Image: "barv1",
					},
				},
			},
		},
	},
}

func TestShouldSpawn(t *testing.T) {
	if _, err := shouldSpawn(dc); err != nil {
		fmt.Printf("error: %+v\n", err)
		t.Fail()
	}
}

func TestShouldNotSpawnBlank(t *testing.T) {
	dc := &v1.DeploymentConfig{}

	if _, err := shouldSpawn(dc); err == nil {
		t.Fail()
	}
}

func TestFailedCanaryShouldNotSpawn(t *testing.T) {
	dc := &v1.DeploymentConfig{}
	anns := map[string]string{
		"canary-fail": "testing",
	}
	dc.SetAnnotations(anns)

	if _, err := shouldSpawn(dc); err == nil {
		t.Fail()
	}
}

func TestCanaryAlreadySpawned(t *testing.T) {
	dc := &v1.DeploymentConfig{}
	anns := map[string]string{
		"canary-pod": "testing",
	}
	dc.SetAnnotations(anns)

	if _, err := shouldSpawn(dc); err == nil {
		t.Fail()
	}
}

func TestGetNameAndImage(t *testing.T) {
	dc := &v1.DeploymentConfig{}
	_, _, err := getNameAndImage(*dc)
	if err == nil {
		t.Fail()
	}

	justName := map[string]string{
		"canary-name": "testing",
	}
	dc.SetAnnotations(justName)
	_, _, err = getNameAndImage(*dc)
	if err == nil {
		t.Fail()
	}

	justImage := map[string]string{
		"canary-image": "testing",
	}
	dc.SetAnnotations(justImage)
	_, _, err = getNameAndImage(*dc)
	if err == nil {
		t.Fail()
	}

	correct := map[string]string{
		"canary-name":  "testing",
		"canary-image": "testing",
	}
	dc.SetAnnotations(correct)
	name, image, err := getNameAndImage(*dc)
	if err != nil {
		t.Fail()
	}
	if name != "testing" || image != "testing" {
		t.Fail()
	}
}

func TestGetImageByName(t *testing.T) {
	containers := []apiv1.Container{
		apiv1.Container{
			Name:  "foo",
			Image: "bar",
		},
	}
	idx, _ := findImage("foo", containers)
	if idx != 0 {
		t.Fail()
	}
	_, err := findImage("notthere", containers)
	if err == nil {
		t.Fail()
	}
}

func TestUpdateObjectMetaDefault(t *testing.T) {
	objMeta := metav1.ObjectMeta{
		Annotations: make(map[string]string),
		Labels:      make(map[string]string),
	}
	updateObjectMeta(&objMeta, dc)
	if objMeta.Labels["canary"] != "true" {
		t.Fail()
	}
	if objMeta.Labels["canary-for"] != "testing" {
		t.Fail()
	}
	if objMeta.Annotations["canary-duration"] != "15m" {
		t.Fail()
	}
}
