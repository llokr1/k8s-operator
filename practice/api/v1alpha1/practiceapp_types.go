/*
Copyright 2026 llokr.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PracticeAppSpec defines the desired state of PracticeApp
type PracticeAppSpec struct {
	// image: 실행할 컨테이너 이미지
	Image string `json:"image"`

	// replicas: 실행할 Pod 수
	Replicas int32 `json:"replicas"`

	// port: 컨테이너가 노출하는 포트 번호
	Port int32 `json:"port"`
}

// PracticeAppStatus defines the observed state of PracticeApp
type PracticeAppStatus struct {
	// phase: Controller가 판단한 현재 상태
	// +optional
	Phase string `json:"phase,omitempty"`

	// message: 현재 상태에 대한 설명
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.port`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// PracticeApp is the Schema for the practiceapps API.
type PracticeApp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of PracticeApp
	// +required
	Spec PracticeAppSpec `json:"spec"`

	// status defines the observed state of PracticeApp
	// +optional
	Status PracticeAppStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PracticeAppList contains a list of PracticeApp.
type PracticeAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PracticeApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PracticeApp{}, &PracticeAppList{})
}
