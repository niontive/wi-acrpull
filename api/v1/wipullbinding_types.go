/*
Copyright 2022.

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

// WIpullbindingSpec defines the desired state of WIpullbinding
type WIpullbindingSpec struct {
	// +kubebuilder:validation:MinLength=0

	// The full server name for the ACR. For example, test.azurecr.io
	AcrServer string `json:"acrServer"`

	// The Service Principal client ID that is used to authenticate with ACR
	ServicePrincipalClientID string `json:"servicePrincipalClientID"`

	// The service principal's tenant ID that is used to authenticate with ACR (if ClientID is specified, this is ignored)
	ServicePrincipalTenantID string `json:"servicePrincipalTenantID"`

	// The Service Account to associate the image pull secret with. If this is not specified, the default Service Account
	// of the namespace will be used.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// WIpullbindingStatus defines the observed state of WIpullbinding
type WIpullbindingStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Information when was the last time the ACR token was refreshed.
	// +optional
	LastTokenRefreshTime *metav1.Time `json:"lastTokenRefreshTime,omitempty"`

	// The expiration date of the current ACR token.
	// +optional
	TokenExpirationTime *metav1.Time `json:"tokenExpirationTime,omitempty"`

	// Error message if there was an error updating the token.
	// +optional
	Error string `json:"error,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// WIpullbinding is the Schema for the wipullbindings API
type WIpullbinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WIpullbindingSpec   `json:"spec,omitempty"`
	Status WIpullbindingStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// WIpullbindingList contains a list of WIpullbinding
type WIpullbindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WIpullbinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WIpullbinding{}, &WIpullbindingList{})
}
