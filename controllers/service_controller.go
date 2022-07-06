/*
Copyright 2021.

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
	"net"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	paasv1 "github.com/paas-team-324/vip-allocator-operator/api/v1"
)

// ServiceReconciler reconciles a Service object
type ServiceReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=services/finalizers,verbs=update

//+kubebuilder:rbac:groups=paas.org,resources=ipgroups,verbs=get;list
//+kubebuilder:rbac:groups=paas.org,resources=ips,verbs=get;list;watch;create;update;patch;delete

// global vars
const ipgroupLabel = "ipgroup"
const serviceIPFinalizer = "ips.paas.org/finalizer"

func (r *ServiceReconciler) patchIP(ipObject *paasv1.IP, service *corev1.Service, ipgroupName string) {
	// set appropriate labels and annotations
	ipObject.Labels = map[string]string{ipgroupLabel: ipgroupName}
	ipObject.Annotations = map[string]string{
		ipAnnotationKey: client.ObjectKeyFromObject(service).String(),
	}
}

func (r *ServiceReconciler) reserveIP(ctx context.Context, ipgroup *paasv1.IPGroup, service *corev1.Service, availableIPs []string) (string, error) {
	// try to reserve an IP until we run out of IPs
	for _, ip := range availableIPs {

		// initialize IP object
		ipObject := &paasv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name: ip,
			},
		}
		r.patchIP(ipObject, service, ipgroup.Name)

		// try creating IP object
		err := r.Create(ctx, ipObject)

		// no error - no problem
		if err == nil {
			return ip, nil

			// if error and it's not AlreadyExists error - report
		} else if err != nil && !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("an error occurred while allocating IP: %v", err)
		}
	}
	// no available ip
	return "", nil
}

func (r *ServiceReconciler) getAvailableIPs(ctx context.Context, ipgroup *paasv1.IPGroup) ([]string, error) {

	// list allocated IPs from given IPGroup
	IPList := &paasv1.IPList{}
	selector := labels.SelectorFromSet(map[string]string{ipgroupLabel: ipgroup.Name})
	if err := r.List(ctx, IPList, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}

	// gather a list of IPs we can't use
	excludedIPs := []string{}
	for _, IP := range IPList.Items {
		excludedIPs = append(excludedIPs, IP.Name)
	}
	excludedIPs = append(excludedIPs, ipgroup.Spec.ExcludedIPs...)

	// parse IPGroup's CIDR field
	ipAddress, ipnet, err := net.ParseCIDR(ipgroup.Spec.Segment)
	if err != nil {
		return nil, err
	}

	// filter out excluded IPs from segment
	var ips []string
	for ipAddress := ipAddress.Mask(ipnet.Mask).To4(); ipnet.Contains(ipAddress); incrementIP(ipAddress) {
		ip := ipAddress.String()
		if !containsString(excludedIPs, ip) {
			ips = append(ips, ip)
		}
	}

	return ips, nil
}

func (r *ServiceReconciler) getIPGroups(ctx context.Context) (*[]paasv1.IPGroup, error) {
	ipgroups := &paasv1.IPGroupList{}
	if err := r.Client.List(ctx, ipgroups, &client.ListOptions{}); err != nil {
		return nil, err
	}

	return &ipgroups.Items, nil
}

func (r *ServiceReconciler) getIPGroupByIP(ctx context.Context, ip string) (*paasv1.IPGroup, error) {
	IP := net.ParseIP(ip)
	if IP == nil {
		return nil, fmt.Errorf("the requested ip could not be parsed")
	}

	IPGroupList, err := r.getIPGroups(ctx)
	if err != nil {
		return nil, err
	}

	for _, ipgroup := range *IPGroupList {
		_, ipnet, err := net.ParseCIDR(ipgroup.Spec.Segment)
		if err != nil {
			return nil, err
		}
		if ipnet.Contains(IP) {
			return &ipgroup, nil
		}
	}
	err = fmt.Errorf("IPGroup not found for the requested ip")
	return nil, err
}

func (r *ServiceReconciler) allocateSpecificIP(ctx context.Context, service *corev1.Service) (string, error) {
	if service.Spec.LoadBalancerIP != "" {
		// get the ipgroup of the requested ip
		ipgroup, err := r.getIPGroupByIP(ctx, service.Spec.LoadBalancerIP)
		if err != nil {
			return "", err
		}

		// check if the ip is available
		availableIPs, err := r.getAvailableIPs(ctx, ipgroup)
		if err != nil {
			return "", err
		}

		if !containsString(availableIPs, service.Spec.LoadBalancerIP) {
			return "", fmt.Errorf("the requested ip is not available")
		}

		// try to reserve the requested ip
		ip, err := r.reserveIP(ctx, ipgroup, service, []string{service.Spec.LoadBalancerIP})
		if err != nil {
			return "", err
		}
		return ip, nil
	}
	return "", nil
}

func (r *ServiceReconciler) allocateAnyIP(ctx context.Context, service *corev1.Service) (string, error) {

	// get all ipgroups
	ipgroups, err := r.getIPGroups(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list IPGroups: %v", err)
	}

	// iterate over all IPGroups
	for _, ipgroup := range *ipgroups {

		// try reserving IP from given IPGroup
		availableIPs, err := r.getAvailableIPs(ctx, &ipgroup)
		if err != nil {
			return "", err
		}

		ip, err := r.reserveIP(ctx, &ipgroup, service, availableIPs)
		if err != nil {
			return "", err
		}

		// if we found an ip we are done
		if ip != "" {
			return ip, nil
		}
	}

	return "", nil
}

func (r *ServiceReconciler) allocateIP(ctx context.Context, service *corev1.Service) (string, error) {

	var ip string
	var err error

	// try to get specific ip
	ip, err = r.allocateSpecificIP(ctx, service)
	if err != nil {
		return "", fmt.Errorf("failed to allocate specific ip: %v", err)
	}

	// check if we got an ip
	if ip != "" {
		return ip, nil
	}

	// try to get any ip
	ip, err = r.allocateAnyIP(ctx, service)
	if err != nil {
		return "", fmt.Errorf("failed to allocate any ip: %v", err)
	}

	// check if we got an ip
	if ip != "" {
		return ip, nil
	}

	return "", fmt.Errorf("no IP could be allocated")
}

func (r *ServiceReconciler) reconcileLBService(ctx context.Context, service *corev1.Service, logger logr.Logger) (ctrl.Result, error) {

	// allocate a new IP address if not present
	if service.Status.LoadBalancer.Ingress == nil {

		var err error
		service.Status.LoadBalancer.Ingress = make([]corev1.LoadBalancerIngress, 1)
		service.Status.LoadBalancer.Ingress[0].IP, err = r.allocateIP(ctx, service)
		if err != nil {
			// TODO how do we relay the error to user?
			return ctrl.Result{}, err
		}

		if err = r.Status().Update(ctx, service); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update service status: %v", err)
		}

		// handle requested load balancer IP mismatch with status
	} else if service.Spec.LoadBalancerIP != "" && service.Spec.LoadBalancerIP != service.Status.LoadBalancer.Ingress[0].IP {
		return r.deleteIP(ctx, service)
	}

	// add IP finalizer to service
	if !controllerutil.ContainsFinalizer(service, serviceIPFinalizer) {
		controllerutil.AddFinalizer(service, serviceIPFinalizer)

		// update object finalizers
		if err := r.Update(ctx, service); err != nil {
			return ctrl.Result{}, fmt.Errorf("could not add IP finalizer to service: %v", err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *ServiceReconciler) deleteIP(ctx context.Context, service *corev1.Service) (ctrl.Result, error) {

	// delete the IP
	if controllerutil.ContainsFinalizer(service, serviceIPFinalizer) {

		// remove IP object from cluster
		if err := r.Delete(ctx, &paasv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name: service.Status.LoadBalancer.Ingress[0].IP,
			},
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete IP object: %v", err)
		}

		// remove IP finalizer and update
		controllerutil.RemoveFinalizer(service, serviceIPFinalizer)
		if err := r.Update(ctx, service); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove IP finalizer from service: %v", err)
		}
	}

	// remove IP from service status if service is not being deleted
	if service.DeletionTimestamp.IsZero() {
		service.Status.LoadBalancer.Ingress = nil
		if err := r.Status().Update(ctx, service); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Service object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("service", req.NamespacedName)

	// get current service from cluster
	service := &corev1.Service{}
	err := r.Client.Get(ctx, req.NamespacedName, service)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// handle service deletion / type change
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer && !service.DeletionTimestamp.IsZero() || // LoadBalancer service is being deleted
		service.Spec.Type != corev1.ServiceTypeLoadBalancer && service.Status.LoadBalancer.Ingress != nil { // service type has been changed from LoadBalancer
		logger.Info("reconciling")

		// remove IP from service
		return r.deleteIP(ctx, service)
	}

	// check if service is of type LoadBalancer
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		logger.Info("reconciling")

		// reconcile the LoadBalancer service object
		return r.reconcileLBService(ctx, service, logger)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Complete(r)
}
