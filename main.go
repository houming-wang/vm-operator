/*
Copyright 2020 easystack.

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

package main

import (
	"flag"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	mixappv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/controllers"
	osservice "easystack.io/vm-operator/pkg/openstack"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	metricsAddr string
	enableLeaderElection bool
	nettpl,vmtpl,vmgtpl,tmpdir string
	pollingPeriod string
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = mixappv1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&metricsAddr, "metrics-addr", "127.0.0.1:9446", "The address the metric endpoint binds to.")
	flag.StringVar(&nettpl, "net-tpl-file", "/etc/vm-operator", "net tpl file path")
	flag.StringVar(&vmtpl, "vm-tpl-file", "/etc/vm-operator", "vm tpl file path")
	flag.StringVar(&vmgtpl, "vmg-tpl-file", "/etc/vm-operator", "vm group tpl file path")
	flag.StringVar(&tmpdir, "tmp-dir", "/tmp", "tmp dir ,should can write ")
	flag.StringVar(&pollingPeriod, "polling-period", "5s", "Polling period of vm status.")

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	oss, err := osservice.NewOSService(nettpl,vmtpl,vmgtpl,tmpdir, ctrl.Log.WithName("OpenStack"),controllers.GetSpecNull)
	if err != nil {
		setupLog.Error(err, "unable to init openstack service")
		os.Exit(1)
	}
	du,err := time.ParseDuration(pollingPeriod)
	if err != nil {
		setupLog.Error(err, "unable to parse duration","str",pollingPeriod)
		os.Exit(1)
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9446,
		LeaderElection:     enableLeaderElection,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	vm := controllers.NewVirtualMachine(mgr.GetClient(), mgr.GetAPIReader(), ctrl.Log.WithName("Controller"), oss)
	err = vm.PollingVmInfo(du)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = vm.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VirtualMachine")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
