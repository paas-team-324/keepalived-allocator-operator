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

	paasv1 "github.com/paas-team-324/keepalived-allocator-operator/api/v1"
)

// VirtualIPReconciler reconciles a VirtualIP object
type VirtualIPReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=paas.org,resources=virtualips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=paas.org,resources=virtualips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=paas.org,resources=virtualips/finalizers,verbs=update

//+kubebuilder:rbac:groups=paas.org,resources=groupsegmentmappings,verbs=get;list
//+kubebuilder:rbac:groups=paas.org,resources=ips,verbs=get;list;watch;create;update;patch;delete

// global vars
const groupSegmentMappingLabel = "gsm"
const ipAnnotationKey = "virtualips.paas.il/owner"
const ipFinalizer = "ip.finalizers.virtualips.paas.org"
const serviceFinalizer = "service.finalizers.virtualips.paas.org"

// general functions
func incrementIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		if ip[j] == 255 {
			ip[j] = 0
		} else {
			ip[j]++
			break
		}
	}
}

func containsString(arr []string, str string) bool {
	for _, item := range arr {
		if item == str {
			return true
		}
	}
	return false
}

func (r *VirtualIPReconciler) patchIP(ipObject *paasv1.IP, virtualIP *paasv1.VirtualIP, gsmName string) {
	// set appropriate labels and annotations
	ipObject.Labels = map[string]string{groupSegmentMappingLabel: gsmName}
	ipObject.Annotations = map[string]string{
		ipAnnotationKey: client.ObjectKeyFromObject(virtualIP).String(),
	}
}

func (r *VirtualIPReconciler) reserveIP(ctx context.Context, groupSegmentMapping *paasv1.GroupSegmentMapping, virtualIP *paasv1.VirtualIP, availableIPs []string) (string, error) {
	// try to reserve an IP until we run out of IPs
	for _, ip := range availableIPs {

		// initialize IP object
		ipObject := &paasv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name: ip,
			},
		}
		r.patchIP(ipObject, virtualIP, groupSegmentMapping.Name)

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

func (r *VirtualIPReconciler) getAvailableIPs(ctx context.Context, groupSegmentMapping *paasv1.GroupSegmentMapping) ([]string, error) {

	// list allocated IPs from given GSM
	IPList := &paasv1.IPList{}
	selector := labels.SelectorFromSet(map[string]string{groupSegmentMappingLabel: groupSegmentMapping.Name})
	if err := r.List(ctx, IPList, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}

	// gather a list of IPs we can't use
	excludedIPs := []string{}
	for _, IP := range IPList.Items {
		excludedIPs = append(excludedIPs, IP.Name)
	}
	excludedIPs = append(excludedIPs, groupSegmentMapping.Spec.ExcludedIPs...)

	// parse GSM's CIDR field
	ipAddress, ipnet, err := net.ParseCIDR(groupSegmentMapping.Spec.Segment)
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

func (r *VirtualIPReconciler) getIPGroups(ctx context.Context) (*[]paasv1.IPGroup, error) {
	ipgroups := &paasv1.IPGroupList{}
	if err := r.Client.List(ctx, ipgroups, &client.ListOptions{}); err != nil {
		return nil, err
	}

	return &ipgroups.Items, nil
}

func (r *VirtualIPReconciler) getGSMs(ctx context.Context) (*[]paasv1.GroupSegmentMapping, error) {
	gsms := &paasv1.GroupSegmentMappingList{}
	if err := r.Client.List(ctx, gsms, &client.ListOptions{}); err != nil {
		return nil, err
	}

	return &gsms.Items, nil
}

func (r *VirtualIPReconciler) getIPGroupByIP(ctx context.Context, ip string) (*paasv1.IPGroup, error) {
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

func (r *VirtualIPReconciler) getGSMByIP(ctx context.Context, ip string) (*paasv1.GroupSegmentMapping, error) {
	IP := net.ParseIP(ip)
	if IP == nil {
		return nil, fmt.Errorf("the requested ip could not be parsed")
	}

	GroupSegmentMappingList, err := r.getGSMs(ctx)
	if err != nil {
		return nil, err
	}

	for _, gsm := range *GroupSegmentMappingList {
		_, ipnet, err := net.ParseCIDR(gsm.Spec.Segment)
		if err != nil {
			return nil, err
		}
		if ipnet.Contains(IP) {
			return &gsm, nil
		}
	}
	err = fmt.Errorf("GroupSegmentMapping not found for the requested ip")
	return nil, err
}

func (r *VirtualIPReconciler) allocateSpecificIP(ctx context.Context, virtualIP *paasv1.VirtualIP) (string, string, string, error) {
	if virtualIP.Spec.IP != "" {
		// get the gsm of the requested ip
		gsm, err := r.getGSMByIP(ctx, virtualIP.Spec.IP)
		if err != nil {
			return "", "", "", err
		}

		// check if the ip is available
		availableIPs, err := r.getAvailableIPs(ctx, gsm)
		if err != nil {
			return "", "", "", err
		}

		if !containsString(availableIPs, virtualIP.Spec.IP) {
			return "", "", "", fmt.Errorf("the requested ip is not available")
		}

		// try to reserve the requested ip
		ip, err := r.reserveIP(ctx, gsm, virtualIP, []string{virtualIP.Spec.IP})
		if err != nil {
			return "", "", "", err
		}
		return ip, gsm.Spec.KeepalivedGroup, gsm.Name, nil
	}
	return "", "", "", nil
}

func (r *VirtualIPReconciler) allocateAnyIP(ctx context.Context, virtualIP *paasv1.VirtualIP) (string, string, string, error) {
	if virtualIP.Spec.IP == "" {
		// get all GSMs
		gsms, err := r.getGSMs(ctx)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to list GroupSegmentMappings: %v", err)
		}

		// iterate over all GSMs
		for _, gsm := range *gsms {

			// try reserving IP from given GSM
			availableIPs, err := r.getAvailableIPs(ctx, &gsm)
			if err != nil {
				return "", "", "", err
			}

			ip, err := r.reserveIP(ctx, &gsm, virtualIP, availableIPs)
			if err != nil {
				return "", "", "", err
			}

			// if we found an ip we are done
			if ip != "" {
				return ip, gsm.Spec.KeepalivedGroup, gsm.Name, nil
			}
		}
	}
	return "", "", "", nil
}

func (r *VirtualIPReconciler) allocateIP(ctx context.Context, virtualIP *paasv1.VirtualIP) (string, string, string, error) {

	var ip string
	var keepalivedGroup string
	var gsmName string

	// try to get specific ip
	ip, keepalivedGroup, gsmName, err := r.allocateSpecificIP(ctx, virtualIP)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to allocate specific ip: %v", err)
	}

	// check if we got an ip
	if ip != "" {
		return ip, keepalivedGroup, gsmName, nil
	}

	// try to get any ip
	ip, keepalivedGroup, gsmName, err = r.allocateAnyIP(ctx, virtualIP)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to allocate any ip: %v", err)
	}

	// check if we got an ip
	if ip != "" {
		return ip, keepalivedGroup, gsmName, nil
	}
	return "", "", "", fmt.Errorf("no IP could be allocated")
}

func (r *VirtualIPReconciler) getOriginalService(ctx context.Context, virtualIP *paasv1.VirtualIP) (*corev1.Service, error) {
	service := &corev1.Service{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: virtualIP.Namespace,
		Name:      virtualIP.Status.Service,
	}, service)
	if err != nil {
		return nil, err
	}
	return service, nil
}

func (r *VirtualIPReconciler) buildKeepalivedClone(virtualIP *paasv1.VirtualIP, service *corev1.Service) error {
	// update the new service
	service.Name = fmt.Sprintf("%s-keepalived-clone", virtualIP.Name)
	service.Spec.ClusterIP = ""
	service.ResourceVersion = ""

	// the clone needs to be of type ClusterIP
	service.Spec.Type = corev1.ServiceTypeClusterIP
	service.Spec.ExternalTrafficPolicy = ""
	if service.Spec.Ports != nil {
		for i := range service.Spec.Ports {
			service.Spec.Ports[i].NodePort = 0
		}
	}

	// set owner reference
	service.OwnerReferences = nil
	err := controllerutil.SetOwnerReference(virtualIP, service, r.Scheme)
	if err != nil {
		return fmt.Errorf("received error while setting service's owner: %v", err)
	}

	// initialize annotations if needed
	if service.Annotations == nil {
		service.Annotations = make(map[string]string)
	}

	// set IP within ExternalIPs field
	service.Spec.ExternalIPs = []string{virtualIP.Status.IP}

	virtualIP.Status.ClonedService = service.Name
	return nil
}

func (r *VirtualIPReconciler) patchService(current *corev1.Service, desired *corev1.Service) {
	if desired.Labels != nil {
		in, out := &desired.Labels, &current.Labels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	} else {
		current.Labels = nil
	}

	if desired.Annotations != nil {
		in, out := &desired.Annotations, &current.Annotations
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	} else {
		current.Annotations = nil
	}

	if desired.Spec.Ports != nil {
		in, out := &desired.Spec.Ports, &current.Spec.Ports
		*out = make([]corev1.ServicePort, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	} else {
		current.Spec.Ports = nil
	}

	if desired.Spec.Selector != nil {
		in, out := &desired.Spec.Selector, &current.Spec.Selector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	} else {
		current.Spec.Selector = nil
	}

	if desired.Spec.SessionAffinityConfig != nil {
		in, out := &desired.Spec.SessionAffinityConfig, &current.Spec.SessionAffinityConfig
		*out = new(corev1.SessionAffinityConfig)
		(*in).DeepCopyInto(*out)
	} else {
		current.Spec.SessionAffinityConfig = nil
	}

	current.Spec.SessionAffinity = desired.Spec.SessionAffinity
	current.Spec.PublishNotReadyAddresses = desired.Spec.PublishNotReadyAddresses
}

func (r *VirtualIPReconciler) updateStatus(ctx context.Context, virtualIP *paasv1.VirtualIP, logger logr.Logger, e error) (ctrl.Result, error) {

	// log error if present
	if e != nil {
		logger.Error(e, "")
		virtualIP.Status.Message = e.Error()
		virtualIP.Status.State = paasv1.StateError
	}

	// do not update status of a VIP that is being deleted
	if virtualIP.DeletionTimestamp.IsZero() {

		if err := r.Status().Update(ctx, virtualIP); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update VirtualIP status: %v", err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *VirtualIPReconciler) deleteVIP(ctx context.Context, virtualIP *paasv1.VirtualIP, logger logr.Logger) (ctrl.Result, error) {

	// delete the cloned service
	if controllerutil.ContainsFinalizer(virtualIP, serviceFinalizer) {

		// remove the cloned service from cluster
		if err := r.Delete(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      virtualIP.Status.ClonedService,
				Namespace: virtualIP.Namespace,
			},
		}); err != nil {
			return r.updateStatus(ctx, virtualIP, logger, err)
		}

		// remove service finalizer and update
		controllerutil.RemoveFinalizer(virtualIP, serviceFinalizer)
		if err := r.Update(ctx, virtualIP); err != nil {
			return r.updateStatus(ctx, virtualIP, logger, err)
		}

		return ctrl.Result{}, nil
	}

	// delete the IP
	if controllerutil.ContainsFinalizer(virtualIP, ipFinalizer) {

		// remove IP object from cluster
		if err := r.Delete(ctx, &paasv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name: virtualIP.Status.IP,
			},
		}); err != nil {
			return r.updateStatus(ctx, virtualIP, logger, err)
		}

		// remove IP finalizer and update
		controllerutil.RemoveFinalizer(virtualIP, ipFinalizer)
		if err := r.Update(ctx, virtualIP); err != nil {
			return r.updateStatus(ctx, virtualIP, logger, err)
		}
	}
	return ctrl.Result{}, nil
}

func (r *VirtualIPReconciler) reconcileIP(ctx context.Context, virtualIP *paasv1.VirtualIP, logger logr.Logger) (ctrl.Result, bool, error) {

	// allocate a new IP address if not present
	if virtualIP.Status.IP == "" {

		// TODO: why can't the bellow line be ":="?
		var err error
		virtualIP.Status.IP, virtualIP.Status.KeepalivedGroup, virtualIP.Status.GSM, err = r.allocateIP(ctx, virtualIP)
		if err != nil {
			result, err := r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not allocate an IP: %v", err))
			return result, true, err
		}

		// update status for the next cycle
		virtualIP.Status.Message = "creating IP object for the service"
		virtualIP.Status.State = paasv1.StateCreatingIP
		result, err := r.updateStatus(ctx, virtualIP, logger, nil)
		return result, true, err
	}

	// add finalizer for IP object
	if !controllerutil.ContainsFinalizer(virtualIP, ipFinalizer) {
		controllerutil.AddFinalizer(virtualIP, ipFinalizer)

		// update object finalizers
		if err := r.Update(ctx, virtualIP); err != nil {
			result, err := r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not add finalizer for IP object: %v", err))
			return result, true, err
		}
		return ctrl.Result{}, true, nil
	}
	return ctrl.Result{}, false, nil
}

func (r *VirtualIPReconciler) reconcileService(ctx context.Context, virtualIP *paasv1.VirtualIP, logger logr.Logger) (ctrl.Result, error) {

	// decide on the service status
	if virtualIP.Status.Service != virtualIP.Spec.Service {
		virtualIP.Status.Service = virtualIP.Spec.Service

		// update status for the next cycle
		virtualIP.Status.Message = "exposing service with an external IP"
		virtualIP.Status.State = paasv1.StateExposing
		return r.updateStatus(ctx, virtualIP, logger, nil)
	}

	// get the original service
	service, err := r.getOriginalService(ctx, virtualIP)
	if err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not get the service to be exposed: %v", err))
	}

	// build the keepalived service's struct
	err = r.buildKeepalivedClone(virtualIP, service)
	if err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("received error while cloning the service: %v", err))
	}

	clone := service.DeepCopy()
	// on create - run the func and create the clone
	// on update - clone will be the current service and then func will run and clone will be updated
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, clone, func() error {
		r.patchService(clone, service)
		return nil
	})
	if err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("failed to create/update the cloned service: %v", err))
	}

	// add service finalizer if not present
	if !controllerutil.ContainsFinalizer(virtualIP, serviceFinalizer) {
		controllerutil.AddFinalizer(virtualIP, serviceFinalizer)

		// update object finalizers
		if err := r.Update(ctx, virtualIP); err != nil {
			return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not add finalizer for service: %v", err))
		}
		return ctrl.Result{}, nil
	}

	virtualIP.Status.Message = "successfully allocated an IP address"
	virtualIP.Status.State = paasv1.StateValid
	return r.updateStatus(ctx, virtualIP, logger, nil)
}

func (r *VirtualIPReconciler) migrateIP(ctx context.Context, virtualIP *paasv1.VirtualIP, logger logr.Logger) (ctrl.Result, error) {

	// get original service from cluster
	service, err := r.getOriginalService(ctx, virtualIP)
	if err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not get the service to be exposed: %v", err))
	}

	// make sure service is not already of type LoadBalancer
	// WARN: if this is true, virtualip will not be fully reconciled until migration is complete!
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer && virtualIP.Status.State != paasv1.StateMigrating {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("service is already of type LoadBalancer, change service type to proceed with migration"))
	}

	// set state to migrating
	if virtualIP.Status.State != paasv1.StateMigrating {
		virtualIP.Status.State = paasv1.StateMigrating
		virtualIP.Status.Message = "VirtualIP is being migrated"

		return r.updateStatus(ctx, virtualIP, logger, nil)
	}

	// get ipgroup by ip
	ipgroup, err := r.getIPGroupByIP(ctx, virtualIP.Status.IP)
	if err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not get IPGroup object of IP: %v", err))
	}

	// get ip object from cluster
	ip := &paasv1.IP{}
	err = r.Client.Get(ctx, client.ObjectKey{
		Name: virtualIP.Status.IP,
	}, ip)
	if err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not get IP object: %v", err))
	}

	// relabel and reannotate ip object
	ip.Labels = map[string]string{ipgroupLabel: ipgroup.ObjectMeta.Name}
	ip.Annotations = map[string]string{
		ipAnnotationKey: client.ObjectKeyFromObject(service).String(),
	}
	if err = r.Client.Update(ctx, ip); err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not update IP object: %v", err))
	}

	// remove IP finalizer if present and update
	if controllerutil.ContainsFinalizer(virtualIP, ipFinalizer) {
		controllerutil.RemoveFinalizer(virtualIP, ipFinalizer)
		if err := r.Update(ctx, virtualIP); err != nil {
			return r.updateStatus(ctx, virtualIP, logger, err)
		}

		return r.updateStatus(ctx, virtualIP, logger, nil)
	}

	// remove service clone and it's finalizer
	if controllerutil.ContainsFinalizer(virtualIP, serviceFinalizer) {
		return r.deleteVIP(ctx, virtualIP, logger)
	}

	// change service type to LoadBalancer with original IP
	service.Spec.Type = corev1.ServiceTypeLoadBalancer
	service.Spec.LoadBalancerIP = virtualIP.Status.IP
	if err := r.Update(ctx, service); err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not change service type to LoadBalancer: %v", err))
	}

	// set ingress IP within the service status to the original IP
	service.Status.LoadBalancer.Ingress = make([]corev1.LoadBalancerIngress, 1)
	service.Status.LoadBalancer.Ingress[0].IP = virtualIP.Status.IP
	if err := r.Status().Update(ctx, service); err != nil {
		return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not set service ingress IP in status: %v", err))
	}

	// if specific IP was not originally requested - remove loadBalancerIP field
	if virtualIP.Spec.IP == "" {

		// get original service
		service, err := r.getOriginalService(ctx, virtualIP)
		if err != nil {
			return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not get the service to be exposed: %v", err))
		}

		service.Spec.LoadBalancerIP = ""
		if err := r.Update(ctx, service); err != nil {
			return r.updateStatus(ctx, virtualIP, logger, fmt.Errorf("could not reset the .spec.loadBalancerIP field in service: %v", err))
		}
	}

	// set virtualip state as migrated
	virtualIP.Status.State = paasv1.StateMigrated
	virtualIP.Status.Message = "VirtualIP has been migrated"
	virtualIP.Status.KeepalivedGroup = ""
	virtualIP.Status.GSM = ""
	virtualIP.Status.ClonedService = ""

	return r.updateStatus(ctx, virtualIP, logger, nil)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VirtualIP object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *VirtualIPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("virtualip", req.NamespacedName)
	logger.Info("reconciling")

	// get current VIP from cluster
	virtualIP := &paasv1.VirtualIP{}
	err := r.Client.Get(ctx, req.NamespacedName, virtualIP)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// check if object is terminating
	if !virtualIP.DeletionTimestamp.IsZero() {
		return r.deleteVIP(ctx, virtualIP, logger)
	}

	// do not reconcile migrated services
	if virtualIP.Status.State == paasv1.StateMigrated {
		return ctrl.Result{}, nil
	}

	// check for migration annotation
	migrationAnnotationPresent := false
	if virtualIP.ObjectMeta.Annotations != nil {
		_, migrationAnnotationPresent = virtualIP.ObjectMeta.Annotations[paasv1.MigrationAnnotation]
	}

	// handle IP migration if migration annotation is present
	if migrationAnnotationPresent {
		return r.migrateIP(ctx, virtualIP, logger)
	}

	// reconcile the IP object
	result, finished, err := r.reconcileIP(ctx, virtualIP, logger)
	if finished {
		return result, err
	}

	// reconcile the service
	return r.reconcileService(ctx, virtualIP, logger)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualIPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&paasv1.VirtualIP{}).
		Complete(r)
}
