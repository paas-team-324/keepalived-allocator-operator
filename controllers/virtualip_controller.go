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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func (r *VirtualIPReconciler) getService(virtualIP *paasv1.VirtualIP) (*corev1.Service, error) {

	// get the service
	service := &corev1.Service{}
	err := r.Client.Get(context.Background(), client.ObjectKey{
		Namespace: virtualIP.Namespace,
		Name:      virtualIP.Status.Service,
	}, service)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("received error while getting service: %v", err))
	}

	// return service
	return service, nil
}

func (r *VirtualIPReconciler) cloneService(virtualIP *paasv1.VirtualIP, clone *corev1.Service) (*corev1.Service, error) {

	// update the new service
	clone.Name = fmt.Sprintf("%s-keepalived-clone", clone.Name)
	clone.Spec.ClusterIP = ""
	clone.ResourceVersion = ""

	// set owner reference
	clone.OwnerReferences = nil
	err := controllerutil.SetOwnerReference(virtualIP, clone, r.Scheme)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("received error while setting service's owner: %v", err))
	}

	return clone, nil
}

func (r *VirtualIPReconciler) patchService(service *corev1.Service, ip string, keepalivedGroup string, remove bool) {

	// initialize annotations if needed
	if service.Annotations == nil {
		service.Annotations = make(map[string]string)
	}

	if !remove {
		// annotate service with keepalived group annotation
		service.Annotations["keepalived-operator.redhat-cop.io/keepalivedgroup"] =
			fmt.Sprintf("%s/%s", keepalivedGroupNamespace, keepalivedGroup)

		// set IP within ExternalIPs field
		service.Spec.ExternalIPs = []string{ip}
	} else {
		delete(service.Annotations, "keepalived-operator.redhat-cop.io/keepalivedgroup")
		service.Spec.ExternalIPs = []string{}
	}
}

func (r *VirtualIPReconciler) reserveIP(groupSegmentMapping *paasv1.GroupSegmentMapping, virtualIP *paasv1.VirtualIP) (string, error) {

	// get list of available IPs within the cluster
	availableIPs, err := r.getAvailableIPs(groupSegmentMapping)
	if err != nil {
		return "", err
	}

	// try to reserve an IP until we run out of IPs
	for _, ip := range availableIPs {

		// try dry creating IP object
		err := r.Create(context.Background(), &paasv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name: ip,
			},
		}, client.DryRunAll)

		// no error - no problem
		if err == nil {
			return ip, nil

			// if error and it's not AlreadyExists error - report
		} else if err != nil && !apierrors.IsAlreadyExists(err) {
			return "", errors.New("an error occurred while allocating IP")
		}
	}

	// could not allocate
	return "", errors.New("there are no available IPs")
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
	var gsmName string

	// allocate IP from given segment
	if virtualIP.Spec.Segment != "" {

		// find matching GSM
		gsm, err := r.getGSMBySegment(virtualIP.Spec.Segment)
		if err != nil {
			return "", "", "", err
		}

		// reserve IP from given GSM
		ip, err = r.reserveIP(gsm, virtualIP)
		if err != nil {
			return "", "", "", err
		}

		// store keepalived group info
		keepalivedGroup = gsm.Spec.KeepalivedGroup
		gsmName = gsm.Name

		// allocate any available IP address
	} else {

		// get all GSMs
		gsms, err := r.getGSMs()
		if err != nil {
			return "", "", "", errors.New(fmt.Sprintf("failed to list GroupSegmentMappings: %v", err))
		}

		// iterate over all GSMs
		for _, gsm := range *gsms {

			// try reserving IP from given GSM
			ip, err = r.reserveIP(&gsm, virtualIP)
			if err != nil {
				return "", "", "", err
			}

			// store keepalived group info
			if ip != "" {
				keepalivedGroup = gsm.Spec.KeepalivedGroup
				gsmName = gsm.Name
				break
			}
		}
	}

	// make sure that we received a valid IP address
	if ip == "" {
		return "", "", "", errors.New("no IP could be allocated")
	}

	return ip, keepalivedGroup, gsmName, nil
}

func (r *VirtualIPReconciler) finishReconciliation(virtualIP *paasv1.VirtualIP, e error) (ctrl.Result, error) {

	if e != nil {
		r.Log.Info(fmt.Sprintf("error: '%v', object: '%+v'", e, virtualIP))
		virtualIP.Status.Message = e.Error()
		virtualIP.Status.State = paasv1.StateError
	} else {
		virtualIP.Status.Message = "successfully allocated an IP address"
		virtualIP.Status.State = paasv1.StateValid
	}

	if virtualIP.DeletionTimestamp.IsZero() {
		if err := r.Status().Update(context.Background(), virtualIP); err != nil {
			r.Log.Info(err.Error())
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
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
	virtualIP := &paasv1.VirtualIP{}
	err := r.Client.Get(context.Background(), req.NamespacedName, virtualIP)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// initialize variables
	deleteVIP := !virtualIP.DeletionTimestamp.IsZero()
	ipFinalizer := "ip.finalizers.virtualips.paas.org"

	// delete IP object for the current VIP
	if deleteVIP {

		// check IP object finalizer
		if controllerutil.ContainsFinalizer(virtualIP, ipFinalizer) {

			// remove IP object from cluster
			if err := r.Delete(context.Background(), &paasv1.IP{
				ObjectMeta: metav1.ObjectMeta{
					Name: virtualIP.Status.IP,
				},
			}); err != nil {
				return r.finishReconciliation(virtualIP, err)
			}

			// remove IP finalizer
			controllerutil.RemoveFinalizer(virtualIP, ipFinalizer)

		}

	} else {

		// allocate a new IP address if not present
		if virtualIP.Status.IP == "" {

			virtualIP.Status.IP, virtualIP.Status.KeepalivedGroup, virtualIP.Status.GSM, err = r.allocateIP(virtualIP)
			if err != nil {
				return r.finishReconciliation(virtualIP, err)
			}
		}

		// ensure object existence
		ipObject := &paasv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name: virtualIP.Status.IP,
			},
		}
		_, err := controllerutil.CreateOrUpdate(context.Background(), r.Client, ipObject, func() error {

			ipObject.Labels = map[string]string{groupSegmentMappingLabel: virtualIP.Status.GSM}
			ipObject.Annotations = map[string]string{
				"virtualips.paas.il/owner": client.ObjectKeyFromObject(virtualIP).String(),
			}

			return nil
		})

		// check for errors
		if err != nil {
			return r.finishReconciliation(virtualIP, errors.New(fmt.Sprintf("could not create/update an IP object: %v", err)))
		}

		// add finalizer for IP object
		controllerutil.AddFinalizer(virtualIP, ipFinalizer)
	}

	// decide on a service name
	if virtualIP.Status.Service == "" {
		virtualIP.Status.Service = virtualIP.Spec.Service
	}

	// get service
	service, err := r.getService(virtualIP)
	if err != nil {
		return r.finishReconciliation(virtualIP, err)
	}

	// check if should clone
	if virtualIP.Status.Clone == nil {
		virtualIP.Status.Clone = &virtualIP.Spec.Clone
	}

	// clone if specified
	if *virtualIP.Status.Clone {
		service, err = r.cloneService(virtualIP, service)
		if err != nil {
			return r.finishReconciliation(virtualIP, errors.New(fmt.Sprintf("received error while cloning service: %v", err)))
		}
	}

	// patch and create/update service
	_, err = controllerutil.CreateOrUpdate(context.Background(), r.Client, service, func() error {
		r.patchService(service, virtualIP.Status.IP, virtualIP.Status.KeepalivedGroup, deleteVIP)
		return nil
	})
	if err != nil {
		return r.finishReconciliation(virtualIP, errors.New(fmt.Sprintf("failed to create/update the service: %v", err)))
	}

	// update object finalizers
	if err := r.Update(context.Background(), virtualIP); err != nil {
		return r.finishReconciliation(virtualIP, err)
	}

	return r.finishReconciliation(virtualIP, nil)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualIPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&paasv1.VirtualIP{}).
		Complete(r)
}
