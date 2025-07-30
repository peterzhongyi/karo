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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IntegrationApiReferencePathSpec struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type IntegrationApiReferenceSpec struct {
	Group              string                          `json:"group"`
	Version            string                          `json:"version"`
	Kind               string                          `json:"kind"`
	Paths              IntegrationApiReferencePathSpec `json:"paths"`
	PropagateTemplates bool                            `json:"propagateTemplates,omitempty"`
}

type IntegrationApiContextRequestSpec struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type IntegrationApiContextSpec struct {
	Name    string                           `json:"name"`
	Request IntegrationApiContextRequestSpec `json:"request"`
}

type IntegrationApiTemplatesSpec struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
}

type IntegrationApiHashSpec struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type IntegrationSpec struct {
	Group      string                        `json:"group"`
	Version    string                        `json:"version"`
	Kind       string                        `json:"kind"`
	References []IntegrationApiReferenceSpec `json:"references,omitempty"`
	Context    []IntegrationApiContextSpec   `json:"context,omitempty"`
	Templates  []IntegrationApiTemplatesSpec `json:"templates"`
	Hashes     []IntegrationApiHashSpec      `json:"hashes"`
}

// IntegrationStatus defines the observed state of Integration
type IntegrationStatus struct {
	Ready bool `json:"ready"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Integration is the Schema for the integrations API
type Integration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   []IntegrationSpec `json:"spec,omitempty"`
	Status IntegrationStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// IntegrationList contains a list of Integration
type IntegrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Integration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Integration{}, &IntegrationList{})
}
