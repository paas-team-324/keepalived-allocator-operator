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
	"errors"
	"fmt"
	"net"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	paasv1 "github.com/vlad-pbr/keepalived-allocator-operator/api/v1"
)

// VirtualIPReconciler reconciles a VirtualIP object
type VirtualIPReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

var groupSegmentMappingLabel = "gsm"
var keepalivedGroupNamespace = "keepalived-operator"

func (r *VirtualIPReconciler) getExposedService(virtualIP *paasv1.VirtualIP) (*corev1.Service, error) {

	// get the service
	clone := &corev1.Service{}
	err := r.Client.Get(context.Background(), client.ObjectKey{
		Namespace: virtualIP.Namespace,
		Name:      virtualIP.Spec.Service,
	}, clone)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("received error while getting service: %v", err))
	}

	// no need to clone - return the service itself
	if !virtualIP.Spec.Clone {
		return clone, nil
	}

	// clone the service and return it
	clone, err = r.cloneService(virtualIP, clone)
	if err != nil {
		return nil, err
	}

	return clone, nil
}

func (r *VirtualIPReconciler) cloneService(virtualIP *paasv1.VirtualIP, clone *corev1.Service) (*corev1.Service, error) {
	if virtualIP == nil {
		return nil, errors.New("function cloneService cannot receive null VirtualIP")
	}

	// update the new service
	clone.Name = fmt.Sprintf("%s-keepalived-clone", clone.Name)
	clone.OwnerReferences = nil
	err := controllerutil.SetOwnerReference(virtualIP, clone, r.Scheme)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("received error while setting service's owner: %v", err))
	}

	return clone, nil
}

func (r *VirtualIPReconciler) patchService(service *corev1.Service, ip string, keepalivedGroup string) {

	// initialize annotations if needed
	if service.Annotations == nil {
		service.Annotations = make(map[string]string)
	}

	// annotate service with keepalived group annotation
	service.Annotations["keepalived-operator.redhat-cop.io/keepalivedgroup"] =
		fmt.Sprintf("%s/%s", keepalivedGroupNamespace, keepalivedGroup)

	// set IP within ExternalIPs field
	service.Spec.ExternalIPs = []string{ip}
}

func (r *VirtualIPReconciler) reserveIP(groupSegmentMapping *paasv1.GroupSegmentMapping) (string, error) {

	// get list of available IPs within the cluster
	availableIPs, err := r.getAvailableIPs(groupSegmentMapping)
	if err != nil {
		return "", err
	}

	// try to reserve an IP until we run out of IPs
	for _, ip := range availableIPs {

		// create new IP object
		ipObject := &paasv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name:   ip,
				Labels: map[string]string{groupSegmentMappingLabel: groupSegmentMapping.Name},
			},
		}

		// try creating IP object
		if err := r.Create(context.Background(), ipObject); err == nil {
			return ip, nil
		}
	}

	// could not allocate
	return "", errors.New("could not allocate an IP")
}

func (r *VirtualIPReconciler) getAvailableIPs(groupSegmentMapping *paasv1.GroupSegmentMapping) ([]string, error) {

	// list allocated IPs from given GSM
	IPList := &paasv1.IPList{}
	selector := labels.SelectorFromSet(map[string]string{groupSegmentMappingLabel: groupSegmentMapping.Name})
	if err := r.List(context.Background(), IPList, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}

	// gather a list of IPs we can't use
	excludedIPs := []string{}
	for _, IP := range IPList.Items {
		excludedIPs = append(excludedIPs, IP.Name)
	}
	for _, ip := range groupSegmentMapping.Spec.ExcludedIPs {
		excludedIPs = append(excludedIPs, ip)
	}

	// parse GSM's CIDR field
	ipAddress, ipnet, err := net.ParseCIDR(groupSegmentMapping.Spec.Segment)
	if err != nil {
		return nil, err
	}

	// filter out excluded IPs from segment
	var ips []string
	for ipAddress := ipAddress.Mask(ipnet.Mask).To4(); ipnet.Contains(ipAddress); incrementIP(ipAddress) {
		ip := ipAddress.String()
		if !contains(excludedIPs, ip) {
			ips = append(ips, ip)
		}
	}

	return ips, nil
}

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

func contains(arr []string, str string) bool {
	for _, item := range arr {
		if item == str {
			return true
		}
	}
	return false
}

func (r *VirtualIPReconciler) getGSMs() (*[]paasv1.GroupSegmentMapping, error) {
	gsms := &paasv1.GroupSegmentMappingList{}
	if err := r.Client.List(context.Background(), gsms, &client.ListOptions{}); err != nil {
		r.Log.Error(err, err.Error())
		return nil, err
	}

	return &gsms.Items, nil
}

func (r *VirtualIPReconciler) getGSMBySegment(segment string) (*paasv1.GroupSegmentMapping, error) {

	GroupSegmentMappingList, err := r.getGSMs()
	if err != nil {
		r.Log.Error(err, err.Error())
		return nil, err
	}

	for _, gsm := range *GroupSegmentMappingList {
		if gsm.Spec.Segment == segment {
			return &gsm, nil
		}
	}

	err = errors.New("GroupSegmentMapping not found for the requested segment")
	r.Log.Error(err, err.Error())
	return nil, err
}

func (r *VirtualIPReconciler) allocateIP(virtualIP *paasv1.VirtualIP) (string, string, string, error) {

	var ip string
	var keepalivedGroup string
	var segment string

	// allocate IP from given segment
	if virtualIP.Spec.Segment != "" {

		// find matching GSM
		gsm, err := r.getGSMBySegment(virtualIP.Spec.Segment)
		if err != nil {
			return "", "", "", err
		}

		// reserve IP from given GSM
		ip, err = r.reserveIP(gsm)
		if err != nil {
			return "", "", "", err
		}

		// store keepalived group info
		keepalivedGroup = gsm.Spec.KeepalivedGroup
		segment = gsm.Spec.Segment

		// allocate any available IP address
	} else {

		// get all GSMs
		gsms, err := r.getGSMs()
		if err != nil {
			return "", "", "", err
		}

		// iterate over all GSMs
		for _, gsm := range *gsms {

			// try reserving IP from given GSM
			ip, err = r.reserveIP(&gsm)
			if err != nil {
				return "", "", "", nil
			}

			// store keepalived group info
			if ip != "" {
				keepalivedGroup = gsm.Spec.KeepalivedGroup
				segment = gsm.Spec.Segment
				break
			}
		}
	}

	// make sure that we received a valid IP address
	if ip == "" {
		virtualIP.Status.Message = "No IP could be allocated"
		return "", "", "", errors.New("No IP could be allocated")
	}

	return ip, keepalivedGroup, segment, nil
}

// +kubebuilder:rbac:groups=paas.org,resources=virtualips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=paas.org,resources=virtualips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=paas.org,resources=virtualips/finalizers,verbs=update

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
	_ = r.Log.WithValues("virtualip", req.NamespacedName)

	// get current VIP from cluster
	var ip, keepalivedGroup, segment string
	virtualIP := &paasv1.VirtualIP{}
	err := r.Client.Get(context.Background(), req.NamespacedName, virtualIP)
	if err != nil {
		return ctrl.Result{}, err
	}

	// allocate a new IP address if not present
	if virtualIP.Status.IP == "" {
		ip, keepalivedGroup, segment, err = r.allocateIP(virtualIP)
		if err != nil {
			return ctrl.Result{}, err
		}

		// retrieve IP from status
	} else {
		ip = virtualIP.Status.IP
		segment = virtualIP.Status.Segment
		gsm, err := r.getGSMBySegment(segment)
		if err != nil {
			return ctrl.Result{}, err
		}

		keepalivedGroup = gsm.Spec.KeepalivedGroup
	}

	// get service to be exposed by external IP
	service, err := r.getExposedService(virtualIP)
	if err != nil {
		return ctrl.Result{}, err
	}

	// patch service with relevant keepalived group info
	r.patchService(service, ip, keepalivedGroup)

	// create/update the service
	_, err = controllerutil.CreateOrUpdate(context.Background(), r.Client, service, func() error { return nil })
	if err != nil {
		return ctrl.Result{}, err
	}

	// update VIP status
	virtualIP.Status.State = paasv1.SUCCEEDED
	virtualIP.Status.Message = "Successfully allocated an IP address"
	virtualIP.Status.IP = ip
	virtualIP.Status.Segment = segment
	err = r.Status().Update(context.Background(), virtualIP)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualIPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&paasv1.VirtualIP{}).
		Complete(r)
}
