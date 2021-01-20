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

// GroupSegmentMappingSpec defines the desired state of GroupSegmentMapping
type GroupSegmentMappingSpec struct {

	// +kubebuilder:validation:Required
	// Name of the matching KeepalivedGroup object
	KeepalivedGroup string `json:"keepalivedGroup,omitempty"`

	// +kubebuilder:validation:Required
	// Segment in which to allocate the IP address
	Segment string `json:"segment,omitempty"`

	// +kubebuilder:validation:Optional
	// Exclude the following IPs from the specified segment
	ExcludedIPs []string `json:"excludedIPs,omitempty"`
}

// GroupSegmentMappingStatus defines the observed state of GroupSegmentMapping
type GroupSegmentMappingStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gsm,scope=Cluster
// +kubebuilder:printcolumn:name="Keepalived Group",type=string,JSONPath=`.spec.keepalivedGroup`
// +kubebuilder:printcolumn:name="Segment",type=string,JSONPath=`.spec.segment`

// GroupSegmentMapping is the Schema for the groupsegmentmappings API
type GroupSegmentMapping struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GroupSegmentMappingSpec   `json:"spec,omitempty"`
	Status GroupSegmentMappingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GroupSegmentMappingList contains a list of GroupSegmentMapping
type GroupSegmentMappingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GroupSegmentMapping `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GroupSegmentMapping{}, &GroupSegmentMappingList{})
}
