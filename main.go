package main

// https://oneuptime.com/blog/post/2026-01-07-go-client-go-kubernetes/view

import (
	//"context"
	"context"
	"fmt"
	"os"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	pods, err := getPods(clientset, "kong")
	if err != nil {
		fmt.Printf("Error getting pods: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Getting pods in kong namespace")
	for _, pod := range pods.Items {
		fmt.Printf("Pod: %s\n", pod.Name)
	}

	// Keep the process alive by sleeping indefinitely
	select {}
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
	// if err != nil {
	// 	//fmt.Printf("Error getting server version: %v\n", err)
	// 	return pods, err
	// }
	return pods, err
}

func isCrashLoop(cs v1.ContainerStatus) bool {
	return cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff"
}
func isOOMKilled(cs v1.ContainerStatus) bool {
	return cs.LastTerminationState.Terminated != nil &&
		cs.LastTerminationState.Terminated.Reason == "OOMKilled"
}

// func handlePodUpdate(oldObj, newObj interface{}) {
// 	pod := newObj.(*v1.Pod)
// 	for _, cs := range pod.Status.ContainerStatuses {
// 		var reason string
// 		switch {
// 		case isCrashLoop(cs):
// 			reason = "CrashLoopBackOff"
// 		case isOOMKilled(cs):
// 			reason = "OOMKilled"
// 		default:
// 			continue
// 		}
// 		key := fmt.Sprintf("%s/%s/%s/%s/%d", pod.Namespace, pod.Name, cs.Name, reason, cs.RestartCount)
// 		if seenRecently(key) {
// 			continue
// 		}

// 		sendDiscord(webhookURL, pod, cs, reason)

// 		if shouldRemediate(pod, reason) {
// 			_ = client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
// 		}
// 	}
// }
