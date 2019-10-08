# Canary Keeper

Canary Keeper is an _operator-like_ service that performs two functions.

1. Executes canary deployments
2. Kills pods that have stopped working

## Canary Deployments

It depends on two non-trivial services to function:

1. prometheus/alertmanager
2. quay.io

To have Canary Keeper manage a deployment you must annotate the deployment with
a label indicating that it should be managedd and two annotations that contain the
pullspec for a quay repo that you wish to test and the container name for that image.

Something kind of like this:

```
apiVersion: v1
kind: DeploymentConfig
metadata:
    name: myapp
    labels:
        app: myapp
        canary: "true"
    annotations:
        canary-image: quay.io/myorg/my_repo:latest
        canary-name: myapp
spec:
    template:
        spec:
            containers:
            - name: myapp
              image: quay.io/myorg/my_repo@sha256:abc...
```

Canary Keeper will compare the image in the podspec with the image referred to
in the `canary` label.  If they are the same, it does nothing and checks back
later, otherwise it executes the canary deployment.

The strategy for the deployment is to deploy a new pod that is a clone of the podspec in the managed deployment with two changes.

1. The container image is the one from the `canary` label.
2. Labels that refer to a deploymentconfig are removed, so that the new pod will not be managed by the deploymentconfig itself.

At this point the pod should share production traffic with the existing
deployment.  This is where the prometheus/alertmanager dependency comes into
play.

Canary Keeper should recieve alerts from alertmanager that reflect SLI breaches
for the managed service.  If the canary receives any alerts then it will be
deemed unfit and the canary will be canceled.  If no such alerts are detected
after the incubation period (15min default) then the managed deployment podspec
will be patched with the new image and the canary terminated.

## Pod Killing

Canary Keeper has another api that simply kills pods that fail to make progress
in ways that in-built readiness checks cannot.  This is enabled via
alertmanager rules as well.  Simply add the `/kill` api as a reciever for a
rule that indicates that a pod has failed and Canary Keeper will attempt to
Delete the pod.
