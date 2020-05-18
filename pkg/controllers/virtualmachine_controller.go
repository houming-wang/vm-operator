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

package controllers

import (
	"context"
	"fmt"
	"sync"
	"time"

	vmv1 "easystack.io/vm-operator/pkg/api/v1"
	"easystack.io/vm-operator/pkg/openstack"
	"github.com/go-logr/logr"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	cli "sigs.k8s.io/controller-runtime/pkg/client"
)

// VirtualMachineReconciler reconciles a VirtualMachine object
type VirtualMachineReconciler struct {
	client    cli.Client
	cliReader cli.Reader
	logger    logr.Logger
	scheme    *runtime.Scheme
	osService *openstack.OSService

	vmCache *vmCache

	ctx     context.Context
	closech chan struct{}

	specs sync.Map
}

type vmCache struct {
	mu sync.Mutex
	// using stackname as key
	vmMap map[string]*vmv1.VirtualMachine
}

func NewVirtualMachine(c cli.Client, r cli.Reader, logger logr.Logger, oss *openstack.OSService) *VirtualMachineReconciler {
	return &VirtualMachineReconciler{
		client:    c,
		cliReader: r,
		logger:    logger,
		ctx:       context.Background(),
		osService: oss,
		vmCache:   &vmCache{vmMap: make(map[string]*vmv1.VirtualMachine)},
		specs:     sync.Map{},
	}
}

func (r *VirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vmv1.VirtualMachine{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mixapp.easystack.io,resources=virtualmachines/status,verbs=get;update;patch

func (r *VirtualMachineReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	var (
		vm     vmv1.VirtualMachine
		status *vmv1.VirtualMachineStatus
		err    error
	)
	err = r.cliReader.Get(r.ctx, req.NamespacedName, &vm)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Delete event
			r.logger.Info("object had deleted", "object", req.String())
			return ctrl.Result{}, nil
		}
		r.logger.Error(err, "will try fetch again", "object", req.String())
		return ctrl.Result{Requeue: true}, err
	}
	if vm.DeletionTimestamp != nil {
		r.logger.Info("object had deleted", "object", req.String())
		//TODO orphan vm
		return ctrl.Result{}, err
	}
	r.logger.Info("phase define", "state", vm.Spec.AssemblyPhase, "object", req.String())
	switch vm.Spec.AssemblyPhase {
	case vmv1.Creating:
		err = openstack.ValidSpec(&vm.Spec)
		if err != nil {
			r.logger.Error(err, "check create spec failed", "object", req.String())
			return ctrl.Result{}, err
		}
		status, err = r.osService.CreateOrUpdate(r.ctx, req.Name, "", &vm.Spec)
		r.specs.Store(req.Name, vm.DeepCopy())
	case vmv1.Updating:
		v, ok := r.specs.Load(req.Name)
		if !ok {
			r.logger.Error(fmt.Errorf("not found"), "can not update when not create", "object", req.String())
			return ctrl.Result{}, err
		}

		err = openstack.ValidUpdateSpec(v.(*vmv1.VirtualMachineSpec), &vm.Spec)
		if err != nil {
			r.logger.Error(err, "check update spec failed", "object", req.String())
			return ctrl.Result{}, err
		}
		r.specs.Store(req.Name, vm.DeepCopy())
		status, err = r.osService.CreateOrUpdate(r.ctx, req.Name, vm.Status.StackID, &vm.Spec)
	case vmv1.Deleting:
		if vm.Status.StackID == "" {
			r.logger.Error(err, "status id not exist, delete required ID and NAME!", "object", req.String())
			return ctrl.Result{}, nil
		}
		status, err = r.osService.Delete(r.ctx, req.Name, vm.Status.StackID, &vm.Spec)
	case vmv1.Failed:
	case vmv1.Succeeded:
	default:
	}
	if err != nil {
		r.logger.Error(err, "osservice failed", "object", req.String())
	}
	ns := status.DeepCopy()
	vm.Status = *ns
	err = r.doUpdateVmCrdStatus(&vm)
	if err != nil {
		r.logger.Error(err, "Failed to update vm")
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineReconciler) Close() {
	close(r.closech)
}

func (r *VirtualMachineReconciler) syncSpec() error {
	var vmList vmv1.VirtualMachineList

	err := r.cliReader.List(r.ctx, &vmList)
	if err != nil {
		r.logger.Error(err, " fetch all sepc failed")
		return err
	}
	for i := range vmList.Items {
		r.specs.Store(vmList.Items[i].Name, vmList.Items[i].DeepCopy())
	}
	return nil
}

func (r *VirtualMachineReconciler) getSpec(name string) *vmv1.VirtualMachine {
	var (
		v  interface{}
		ok bool
	)
	v, ok = r.specs.Load(name)
	if !ok {
		r.syncSpec()
		v, ok = r.specs.Load(name)
	}
	if !ok {
		return nil
	}
	return v.(*vmv1.VirtualMachine)
}

func (r *VirtualMachineReconciler) PollingVmInfo(du time.Duration) error {
	r.osService.PollingForever(r.ctx, du, func(stack *openstack.StatStack) error {
		vm := r.getSpec(stack.Name)
		if vm == nil {
			r.logger.Info("not found vm crd nifo", "name", stack.Name)
			return nil
		}
		vm.Status.StackID = stack.Id
		switch stack.Status {
		case openstack.S_DELETE_FAILED:
			fallthrough
		case openstack.S_UPDATE_FAILED:
			fallthrough
		case openstack.S_CREATE_FAILED:
			vm.Status.StackID = stack.Id
			vm.Status.VmStatus = vmv1.Failed
			vm.Spec.AssemblyPhase = vmv1.Failed
			r.logger.Error(fmt.Errorf(stack.Statusreason), "stack failed", "name", stack.Name, "id", stack.Id)
		case openstack.S_CREATE_COMPLETE:
			fallthrough
		case openstack.S_UPDATE_COMPLETE:
			fallthrough
		case openstack.S_DELETE_COMPLETE:
			vm.Status.VmStatus = vmv1.Succeeded
			vm.Spec.AssemblyPhase = vmv1.Succeeded
		default:
		}
		return r.doUpdateVmCrdStatus(vm)
	})
	return nil
}
func (r *VirtualMachineReconciler) doUpdateVmCrdStatus(vm *vmv1.VirtualMachine) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.client.Update(r.ctx, vm); err != nil {
			return err
		}
		return nil
	})
}

func (v *vmCache) get(key string) (*vmv1.VirtualMachine, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	vm, ok := v.vmMap[key]
	return vm, ok
}

func (v *vmCache) set(key string, vm *vmv1.VirtualMachine) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.vmMap[key] = vm
}

func (v *vmCache) del(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.vmMap, key)
}

func GetSpecNull(name string) *vmv1.VirtualMachine {
	return nil
}
