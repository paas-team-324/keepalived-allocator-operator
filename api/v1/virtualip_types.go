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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// VirtualIPSpec defines the desired state of VirtualIP
type VirtualIPSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	// Name of the service to be exposed
	Service string `json:"service"`

	// +kubebuilder:validation:Optional
	// Segment in which to allocate the IP address
	Segment string `json:"segment,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	// Whether to clone the service or not
	Clone bool `json:"clone"`
}

type VirtualIPState string

const (
	SUCCEEDED VirtualIPState = "Succeeded"
	FAILED    VirtualIPState = "Failed to allocate"
	RESERVING VirtualIPState = "Reserving IP"
	CLONING   VirtualIPState = "Cloning service"
	UPDATING  VirtualIPState = "Updating service"
)

// VirtualIPStatus defines the observed state of VirtualIP
type VirtualIPStatus struct {
	Message         string         `json:"message,omitempty"`
	IP              string         `json:"IP,omitempty"`
	KeepalivedGroup string         `json:"keepalivedGroup,omitempty"`
	State           VirtualIPState `json:"state,omitempty"`
	Service         string         `json:"service,omitempty"`
	Clone           *bool          `json:"clone,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vip,scope=Namespaced
// +kubebuilder:printcolumn:name="Service",type=string,JSONPath=`.status.service`
// +kubebuilder:printcolumn:name="IP",type=string,JSONPath=`.status.IP`

// VirtualIP is the Schema for the virtualips API
type VirtualIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualIPSpec   `json:"spec,omitempty"`
	Status VirtualIPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualIPList contains a list of VirtualIP
type VirtualIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualIP `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VirtualIP{}, &VirtualIPList{})
}
