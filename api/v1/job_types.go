/*
Copyright 2024.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// JobSpec defines the desired state of Job
type JobSpec struct {
	// +kubebuilder:validation:MinItems:=1
	Events []Event `json:"events"`
}

// +kubebuilder:validation:XValidation:message="script or wasm must be specified", rule=((self.script!="" && self.wasm=="") || (self.script=="" && self.wasm!=""))
type Event struct {
	// +kubebuilder:validation:MinLength:=1
	Name string `json:"name"`

	Wasm string `json:"wasm"`

	Script string `json:"script"`

	// +kubebuilder:validation:XValidation:message="schema.key or schema.name must be specified", rule=(self.key!="" || self.name!="")
	Schema corev1.ConfigMapKeySelector `json:"schema"`
}

// JobStatus defines the observed state of Job
type JobStatus struct {
	Active []corev1.ObjectReference `json:"active,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Job is the Schema for the jobs API
type Job struct {
	client            *ClientWrapper
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   JobSpec   `json:"spec"`
	Status JobStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen=false
type ClientWrapper struct {
	client.Client
}

func (c *ClientWrapper) DeepCopyInto(cc *ClientWrapper) {
	cc.Client = c.Client
}

func (c *ClientWrapper) DeepCopy() *ClientWrapper {
	return c
}

func NewJob(c client.Client) *Job {
	return &Job{
		client: &ClientWrapper{c},
	}
}

//+kubebuilder:object:root=true

// JobList contains a list of Job
type JobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Job `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Job{}, &JobList{})
}
