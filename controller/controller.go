/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"time"

	l "github.com/redhatinsights/miniop/logger"
	"go.uber.org/zap"
	r "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type Worker interface {
	Work(*Controller, string) error
}

type Controller struct {
	Indexer  cache.Indexer
	Queue    workqueue.RateLimitingInterface
	Informer cache.Controller
	Worker   Worker
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.Queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two pods with the same key are never processed in
	// parallel.
	defer c.Queue.Done(key)

	// Invoke the method containing the business logic
	err := c.Worker.Work(c, key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.Queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.Queue.NumRequeues(key) < 5 {
		l.Log.Info(fmt.Sprintf("Error syncing deploymentconfig"), zap.Reflect("deploymentconfig", key), zap.Error(err))

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.Queue.AddRateLimited(key)
		return
	}

	c.Queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	l.Log.Info(fmt.Sprintf("Dropping pod %q out of the queue", key), zap.Error(err))
}

// Run comment
func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer c.Queue.ShutDown()
	l.Log.Info("Starting DeploymentConfig controller")

	go c.Informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.Informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	l.Log.Info("Stopping DeploymentConfig controller")
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

// Start comment
func Start(lw cache.ListerWatcher, objType r.Object, worker Worker) {
	// create the workqueue
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	// Bind the workqueue to a cache with the help of an informer. This way we make sure that
	// whenever the cache is updated, the pod key is added to the workqueue.
	// Note that when we finally process the item from the workqueue, we might see a newer version
	// of the Pod than the version which was responsible for triggering the update.
	indexer, informer := cache.NewIndexerInformer(lw, objType, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	}, cache.Indexers{})

	controller := &Controller{
		Indexer:  indexer,
		Queue:    queue,
		Informer: informer,
		Worker:   worker,
	}

	// Now let's start the controller
	stop := make(chan struct{})
	defer close(stop)
	go controller.Run(1, stop)

	// Wait forever
	select {}
}
