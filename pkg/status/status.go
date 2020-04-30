// Copyright 2019 The Cluster Monitoring Operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package status

import (
	"fmt"
	"strings"

	"github.com/Jeffail/gabs"
	routev1 "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/openshift/cluster-monitoring-operator/test/e2e/framework"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// IsDegraded returns true when there are any critical alerts firing in openshift-monitoring
// or openshift-user-workload-monitoring namespaces. Or when quering of alerts fails as that
// implies a problem somewhere in the cluster.
func IsDegraded(config *rest.Config) (bool, string, error) {
	firing, msg, err := alertsFiring(config)
	if err != nil {
		return true, msg, errors.Wrap(err, "could not query for alerts firing")
	}
	if firing {
		return true, "MonitoringAlertsFiring", errors.New("alerts around monitoring stack are firing")
	}
	return false, "", nil
}

func alertsFiring(config *rest.Config) (bool, string, error) {
	// Prometheus client depends on setup above.
	// So far only necessary for prometheusK8sClient.
	openshiftRouteClient, err := routev1.NewForConfig(config)
	if err != nil {
		return false, "OpenShiftRouteClientError", errors.Wrap(err, "creating openshiftRouteClient failed")
	}
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, "KubeConfigError", errors.Wrap(err, "creating kubeClient failed")
	}
	token, err := getServiceAccountToken(kubeClient, "openshift-monitoring", "cluster-monitoring-operator")
	if err != nil {
		return false, "ServiceAccountTokenMissing", err
	}
	thanosQuerierClient, err := framework.NewPrometheusClientFromRoute(
		openshiftRouteClient,
		"openshift-monitoring", "thanos-querier",
		token,
	)
	if err != nil {
		return false, "ThanosQuerierClientError", errors.Wrap(err, "creating ThanosQuerierClient failed")
	}
	// TODO: replace with actual alerts that we care about
	// critical in openshift-monitoring and openshift-user-workload-monitoring
	// if user workload is enabled then check that namespace as well
	// Any critical monitoring alert
	body, err := thanosQuerierClient.PrometheusQuery(`ALERTS{namespace="openshift-monitoring", severity="critical"}`)
	if err != nil {
		fmt.Println(err)
		return false, "ThanosQuerierQueryFailed", err
	}

	res, err := gabs.ParseJSON(body)
	if err != nil {
		fmt.Println(err)
		return false, "", err
	}

	count, err := res.ArrayCountP("data.result")
	if err != nil {
		fmt.Println(err)
		return false, "", err
	}

	if count > 0 {
		fmt.Println(res)
		fmt.Println("----what")
		return true, "AlertsFiring", nil
	}

	return false, "", nil
}

func getServiceAccountToken(kubeClient *kubernetes.Clientset, namespace, name string) (string, error) {
	secrets, err := kubeClient.CoreV1().Secrets(namespace).List(metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, secret := range secrets.Items {
		_, dockerToken := secret.Annotations["openshift.io/create-dockercfg-secrets"]
		token := strings.Contains(secret.Name, fmt.Sprintf("%s-token-", name))

		// we have to skip the token secret that contains the openshift.io/create-dockercfg-secrets annotation
		// as this is the token to talk to the internal registry.
		if !dockerToken && token {
			return string(secret.Data["token"]), nil
		}
	}
	return "", errors.Errorf("cannot find token for %s/%s service account", namespace, name)
}
