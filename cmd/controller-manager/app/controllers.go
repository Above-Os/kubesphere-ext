/*
Copyright 2019 The KubeSphere Authors.

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

package app

import (
	"fmt"
	"time"

	"github.com/kubesphere/pvc-autoresizer/runners"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
	"kubesphere.io/kubesphere/cmd/controller-manager/app/options"
	"kubesphere.io/kubesphere/pkg/controller/namespace"
	"kubesphere.io/kubesphere/pkg/controller/user"
	"kubesphere.io/kubesphere/pkg/models/kubeconfig"
	ldapclient "kubesphere.io/kubesphere/pkg/simple/client/ldap"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"kubesphere.io/kubesphere/pkg/controller/clusterrolebinding"
	"kubesphere.io/kubesphere/pkg/informers"
	"kubesphere.io/kubesphere/pkg/simple/client/k8s"
)

var allControllers = []string{
	"user",
	"workspacetemplate",
	"workspace",
	"workspacerole",
	"workspacerolebinding",
	"namespace",

	"serviceaccount",
	"resourcequota",

	"storagecapability",
	"volumesnapshot",
	"pvcautoresizer",
	"workloadrestart",
	"nsnp",

	"clusterrolebinding",

	"fedglobalrolecache",
	"globalrole",
	"fedglobalrolebindingcache",
	"globalrolebinding",

	"notification",
}

// setup all available controllers one by one
func addAllControllers(mgr manager.Manager, client k8s.Client, informerFactory informers.InformerFactory,
	cmOptions *options.KubeSphereControllerManagerOptions,
	stopCh <-chan struct{}) error {
	var err error

	////////////////////////////////////
	// begin init necessary informers
	////////////////////////////////////
	kubernetesInformer := informerFactory.KubernetesSharedInformerFactory()
	kubesphereInformer := informerFactory.KubeSphereSharedInformerFactory()
	////////////////////////////////////
	// end informers
	////////////////////////////////////

	////////////////////////////////////
	// begin init necessary clients
	////////////////////////////////////
	kubeconfigClient := kubeconfig.NewOperator(client.Kubernetes(),
		informerFactory.KubernetesSharedInformerFactory().Core().V1().ConfigMaps().Lister(),
		client.Config())

	var ldapClient ldapclient.Interface
	// when there is no ldapOption, we set ldapClient as nil, which means we don't need to sync user info into ldap.
	if cmOptions.LdapOptions != nil && len(cmOptions.LdapOptions.Host) != 0 {
		if cmOptions.LdapOptions.Host == ldapclient.FAKE_HOST { // for debug only
			ldapClient = ldapclient.NewSimpleLdap()
		} else {
			ldapClient, err = ldapclient.NewLdapClient(cmOptions.LdapOptions, stopCh)
			if err != nil {
				return fmt.Errorf("failed to connect to ldap service, please check ldap status, error: %v", err)
			}
		}
	} else {
		klog.Warning("ks-controller-manager starts without ldap provided, it will not sync user into ldap")
	}
	////////////////////////////////////
	// end init clients
	////////////////////////////////////

	////////////////////////////////////////////////////////
	// begin init controller and add to manager one by one
	////////////////////////////////////////////////////////

	// "user" controller
	if cmOptions.IsControllerEnabled("user") {
		userController := &user.Reconciler{
			MaxConcurrentReconciles: 4,
			LdapClient:              ldapClient,
			KubeconfigClient:        kubeconfigClient,
			AuthenticationOptions:   cmOptions.AuthenticationOptions,
		}
		addControllerWithSetup(mgr, "user", userController)
	}

	// "namespace" controller
	if cmOptions.IsControllerEnabled("namespace") {
		namespaceReconciler := &namespace.Reconciler{}
		addControllerWithSetup(mgr, "namespace", namespaceReconciler)
	}

	// "pvc-autoresizer"
	monitoringOptionsEnable := cmOptions.MonitoringOptions != nil && len(cmOptions.MonitoringOptions.Endpoint) != 0
	if monitoringOptionsEnable {
		if cmOptions.IsControllerEnabled("pvc-autoresizer") {
			if err := runners.SetupIndexer(mgr, false); err != nil {
				return err
			}
			promClient, err := runners.NewPrometheusClient(cmOptions.MonitoringOptions.Endpoint)
			if err != nil {
				return err
			}
			pvcAutoResizerController := runners.NewPVCAutoresizer(
				promClient,
				mgr.GetClient(),
				ctrl.Log.WithName("pvc-autoresizer"),
				1*time.Minute,
				mgr.GetEventRecorderFor("pvc-autoresizer"),
			)
			addController(mgr, "pvcautoresizer", pvcAutoResizerController)
		}
	}

	if cmOptions.IsControllerEnabled("pvc-workload-restarter") {
		restarter := runners.NewRestarter(
			mgr.GetClient(),
			ctrl.Log.WithName("pvc-workload-restarter"),
			1*time.Minute,
			mgr.GetEventRecorderFor("pvc-workload-restarter"),
		)
		addController(mgr, "pvcworkloadrestarter", restarter)
	}

	// "clusterrolebinding" controller
	if cmOptions.IsControllerEnabled("clusterrolebinding") {
		clusterRoleBindingController := clusterrolebinding.NewController(client.Kubernetes(),
			kubernetesInformer.Rbac().V1().ClusterRoleBindings(),
			kubernetesInformer.Apps().V1().Deployments(),
			kubernetesInformer.Core().V1().Pods(),
			kubesphereInformer.Iam().V1alpha2().Users(),
			cmOptions.AuthenticationOptions.KubectlImage)
		addController(mgr, "clusterrolebinding", clusterRoleBindingController)
	}

	// log all controllers process result
	for _, name := range allControllers {
		if cmOptions.IsControllerEnabled(name) {
			if addSuccessfullyControllers.Has(name) {
				klog.Infof("%s controller is enabled and added successfully.", name)
			} else {
				klog.Infof("%s controller is enabled but is not going to run due to its dependent component being disabled.", name)
			}
		} else {
			klog.Infof("%s controller is disabled by controller selectors.", name)
		}
	}

	return nil
}

var addSuccessfullyControllers = sets.NewString()

type setupableController interface {
	SetupWithManager(mgr ctrl.Manager) error
}

func addControllerWithSetup(mgr manager.Manager, name string, controller setupableController) {
	if err := controller.SetupWithManager(mgr); err != nil {
		klog.Fatalf("Unable to create %v controller: %v", name, err)
	}
	addSuccessfullyControllers.Insert(name)
}

func addController(mgr manager.Manager, name string, controller manager.Runnable) {
	if err := mgr.Add(controller); err != nil {
		klog.Fatalf("Unable to create %v controller: %v", name, err)
	}
	addSuccessfullyControllers.Insert(name)
}
