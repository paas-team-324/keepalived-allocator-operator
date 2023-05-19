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

package v1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var virtualiplog = logf.Log.WithName("virtualip-resource")

func (r *VirtualIP) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-paas-org-v1-virtualip,mutating=false,failurePolicy=fail,sideEffects=None,groups=paas.org,resources=virtualips,verbs=create;update,versions=v1,name=vvirtualip.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &VirtualIP{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *VirtualIP) ValidateCreate() error {
	virtualiplog.Info("validate create", "name", r.Name)

	return fmt.Errorf("VirtualIP resource is deprecated, use a service with type LoadBalancer instead")
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *VirtualIP) ValidateUpdate(old runtime.Object) error {
	virtualiplog.Info("validate update", "name", r.Name)

	oldVip := old.(*VirtualIP)

	// check for migration annotation in both iterations
	migrationAnnotationPresentOld := false
	migrationAnnotationPresentNew := false
	if oldVip.ObjectMeta.Annotations != nil {
		_, migrationAnnotationPresentOld = oldVip.ObjectMeta.Annotations[MigrationAnnotation]
	}
	if r.ObjectMeta.Annotations != nil {
		_, migrationAnnotationPresentNew = r.ObjectMeta.Annotations[MigrationAnnotation]
	}

	// forbid backing out of migration
	if migrationAnnotationPresentOld && !migrationAnnotationPresentNew {
		return fmt.Errorf("VirtualIP migration can not be stopped")
	}

	// forbid migration of non-valid service
	if !migrationAnnotationPresentOld && migrationAnnotationPresentNew && r.Status.State != StateValid {
		return fmt.Errorf("not valid VirtualIP can not be migrated")
	}

	// forbid service change during migration
	if migrationAnnotationPresentOld && r.Spec.Service != oldVip.Spec.Service {
		return fmt.Errorf("can not change service once migration has begun")
	}

	// ensure .spec.ip immutability
	if r.Spec.IP != oldVip.Spec.IP {
		return fmt.Errorf(".spec.ip is immutable")
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *VirtualIP) ValidateDelete() error {
	virtualiplog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}
