package main

// https://oneuptime.com/blog/post/2026-01-07-go-client-go-kubernetes/view

import (
	"context"
	"fmt"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

func main() {
	// InClusterConfig uses the service account token and CA certificate
	// mounted at /var/run/secrets/kubernetes.io/serviceaccount/
	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Printf("Error getting in-cluster config: %v\n", err)
		os.Exit(1)
	}

	// Create clientset with the in-cluster configuration
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating clientset: %v\n", err)
		os.Exit(1)
	}

	// Verify connectivity by getting server version
	version, err := clientset.Discovery().ServerVersion()
	if err != nil {
		fmt.Printf("Error getting server version: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Connected to Kubernetes %s\n", version.GitVersion)

	fmt.Printf("Monitoring pods in kong namespace\n")
	monitorPods(clientset, "kong")

	// Keep the process alive by sleeping indefinitely
	select {}
}

// monitorPods watches pods in a namespace and detects status changes
func monitorPods(clientset *kubernetes.Clientset, namespace string) {

	// Use k8s informers to continuously monitor pods. Resyncs every 30s

	factory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		30*time.Second,
		informers.WithNamespace(namespace),
	)

	podInformer := factory.Core().V1().Pods().Informer()

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		// log new pods and check status
		AddFunc: func(obj interface{}) {
			pod := obj.(*v1.Pod)
			fmt.Printf("[ADD] Pod: %s/%s\n", pod.Namespace, pod.Name)
			checkPodStatus(pod)
		},
		// log status changes
		UpdateFunc: func(oldObj, newObj interface{}) {
			newPod := newObj.(*v1.Pod)
			oldPod := oldObj.(*v1.Pod)

			// Only process if status changed
			if oldPod.Status.Phase != newPod.Status.Phase ||
				hasContainerStatusChanged(oldPod.Status.ContainerStatuses, newPod.Status.ContainerStatuses) {
				fmt.Printf("[UPDATE] Pod: %s/%s - Phase: %s\n", newPod.Namespace, newPod.Name, newPod.Status.Phase)
				checkPodStatus(newPod)
			}
		},
		// log pod deletions
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*v1.Pod)
			fmt.Printf("[DELETE] Pod: %s/%s\n", pod.Namespace, pod.Name)
		},
	})

	stop := make(chan struct{})
	factory.Start(stop)
	factory.WaitForCacheSync(stop)
}

// checkPodStatus examines a pod and reports any issues
func checkPodStatus(pod *v1.Pod) {
	// Check overall pod phase
	switch pod.Status.Phase {
	case v1.PodPending:
		fmt.Printf("  ⏳ Pod is Pending\n")
	case v1.PodRunning:
		fmt.Printf("  ✓ Pod is Running\n")
	case v1.PodSucceeded:
		fmt.Printf("  ✓ Pod Succeeded\n")
	case v1.PodFailed:
		fmt.Printf("  ✗ Pod Failed - Reason: %s\n", pod.Status.Reason)
	case v1.PodUnknown:
		fmt.Printf("  ? Pod Unknown\n")
	}

	// Check each container status for issues
	for i, containerStatus := range pod.Status.ContainerStatuses {
		if isCrashLoop(containerStatus) {
			fmt.Printf("  ⚠️  Container %d (%s) is in CrashLoopBackOff (Restarts: %d)\n",
				i, containerStatus.Name, containerStatus.RestartCount)
		} else if isOOMKilled(containerStatus) {
			fmt.Printf("  💥 Container %d (%s) was OOMKilled\n", i, containerStatus.Name)
		} else if containerStatus.State.Waiting != nil {
			fmt.Printf("  ⏳ Container %d (%s) is Waiting - Reason: %s\n",
				i, containerStatus.Name, containerStatus.State.Waiting.Reason)
		} else if containerStatus.State.Terminated != nil {
			fmt.Printf("  ⏹️  Container %d (%s) is Terminated - Reason: %s (Exit Code: %d)\n",
				i, containerStatus.Name, containerStatus.State.Terminated.Reason,
				containerStatus.State.Terminated.ExitCode)
		}
	}
}

// hasContainerStatusChanged checks if any container status changed. Prevents duplicate alerts
func hasContainerStatusChanged(oldStatuses, newStatuses []v1.ContainerStatus) bool {
	if len(oldStatuses) != len(newStatuses) {
		return true
	}

	for i, newStatus := range newStatuses {
		if i >= len(oldStatuses) {
			return true
		}
		oldStatus := oldStatuses[i]

		if oldStatus.RestartCount != newStatus.RestartCount {
			return true
		}

		// Compare state
		if (oldStatus.State.Waiting != nil) != (newStatus.State.Waiting != nil) {
			return true
		}
		if (oldStatus.State.Running != nil) != (newStatus.State.Running != nil) {
			return true
		}
		if (oldStatus.State.Terminated != nil) != (newStatus.State.Terminated != nil) {
			return true
		}

		// Compare last termination state
		if (oldStatus.LastTerminationState.Terminated != nil) != (newStatus.LastTerminationState.Terminated != nil) {
			return true
		}
	}

	return false
}

// Check for specific error types
func handleError(err error) {
	if errors.IsNotFound(err) {
		// Resource doesn't exist - may be expected
		fmt.Println("Resource not found")
	} else if errors.IsConflict(err) {
		// Concurrent modification - retry with updated version
		fmt.Println("Conflict detected, retry needed")
	} else if errors.IsServerTimeout(err) {
		// API server timeout - retry with backoff
		fmt.Println("Server timeout, retrying...")
	} else if errors.IsTooManyRequests(err) {
		// Rate limited - respect backoff
		fmt.Println("Rate limited, backing off")
	}
}

func getPods(client *kubernetes.Clientset, namespace string) (*v1.PodList, error) {
	pods, err := client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	return pods, err
}

func isCrashLoop(cs v1.ContainerStatus) bool {
	return cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff"
}

func isOOMKilled(cs v1.ContainerStatus) bool {
	return cs.LastTerminationState.Terminated != nil &&
		cs.LastTerminationState.Terminated.Reason == "OOMKilled"
}
