// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Leader Election client implementation that uses the built-in Kubernetes leaderelection
// package. Intended to be deployed as sidecar alongside a separate container, which is notified
// via a configured HTTP webhook when it is elected leader.
//
// Adapted from https://github.com/kubernetes/client-go/blob/master/examples/leader-election/main.go.
// See also https://github.com/kubernetes/client-go/blob/kubernetes-1.14.8/tools/leaderelection/leaderelection.go
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes/typed/core/v1"
)

var (
	id            = flag.String("id", uuid.New().String(), "This pod's participant ID for leader election")
	port          = flag.Int("electionPort", 4040, "Required. Send HTTP leader election notifications to this port")
	lockName      = flag.String("lockName", "", "the lock resource name")
	lockNamespace = flag.String("lockNamespace", "default", "the lock resource namespace")
	leaseDuration = flag.Duration("leaseDuration", 4, "time (seconds) that non-leader candidates will wait to force acquire leadership")
	renewDeadline = flag.Duration("renewDeadline", 2, "time (seconds) that the acting leader will retry refreshing leadership before giving up")
	retryPeriod   = flag.Duration("retryPeriod", 1, "time (seconds) LeaderElector candidates should wait between tries of actions")
	leaderID      string
)

func buildConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func main() {
	flag.Parse()
	klog.InitFlags(nil)

	if *port <= 0 {
		klog.Fatal("Missing --electionPort flag")
	}
	if *lockName == "" {
		klog.Fatal("Missing --lockName flag.")
	}
	if *lockNamespace == "" {
		klog.Fatal("Missing --lockNamespace flag.")
	}

	// Leader election uses the Kubernetes API by writing to a lock object.
	// Conflicting writes are detected and each client handles those actions
	// independently.
	config, err := buildConfig()
	if err != nil {
		klog.Fatal(err)
	}
	client := clientset.NewForConfigOrDie(config)

	// Use a Go context so we can tell the leaderelection code when we
	// want to step down
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen for interrupts or the Linux SIGTERM signal and cancel
	// our context, which the leader election code will observe and
	// step down
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		klog.Infof("Received termination, stopping leader election for %s", *id)
		cancel()
	}()

	lock := &resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Name:      *lockName,
			Namespace: *lockNamespace,
		},
		Client: client,
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: *id,
		},
	}

	klog.Infof("Commencing leader election for candidate %s", *id)
	listenerRootURL := fmt.Sprintf("http://localhost:%d", *port)
	startURL := fmt.Sprintf("%s/start", listenerRootURL)
	stopURL := fmt.Sprintf("%s/stop", listenerRootURL)
	klog.Infof("Will notify listener at %s of leader changes", listenerRootURL)

	// Start the leader election code loop.
	// Notify changes in leader status via the listener webhook
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: lock,
		// IMPORTANT: you MUST ensure that any code you have that
		// is protected by the lease must terminate **before**
		// you call cancel. Otherwise, you could have a background
		// loop still running and another process could
		// get elected before your background loop finished, violating
		// the stated goal of the lease.
		ReleaseOnCancel: true,
		LeaseDuration:   *leaseDuration * time.Second,
		RenewDeadline:   *renewDeadline * time.Second,
		RetryPeriod:     *retryPeriod * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// This pod is now the leader! Notify listener
				klog.Infof("Notifying %s started leading", *id)
				resp, err := http.Get(startURL)
				if err != nil {
					klog.Errorf("Failed to notify leader of start: %v", err)
				}
				defer resp.Body.Close()
			},
			OnStoppedLeading: func() {
				// This pod stopped leading! Notify listener
				klog.Infof("Notifying %s stopped leading", *id)
				resp, err := http.Get(stopURL)
				if err != nil && ctx.Err() == nil {
					klog.Errorf("Failed to notify leader of stop: %v", err)
				}
				defer resp.Body.Close()
			},
			OnNewLeader: func(identity string) {
				// The leader has changed
				leaderID = identity
				klog.Infof("%s elected as new leader", identity)
			},
		},
	})

}
