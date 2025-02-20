/*
Copyright The Kubernetes Authors.

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

package v1alpha5

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RemediationType string

const (
	// RemediationFinalizer allows Metal3RemediationReconciler to clean up resources associated with Metal3Remediation before
	// removing it from the apiserver.
	RemediationFinalizer = "metal3remediation.infrastructure.cluster.x-k8s.io"

	// RebootRemediationStrategy sets RemediationType to Reboot.
	RebootRemediationStrategy RemediationType = "Reboot"
)

const (
	// PhaseRunning represents the running state during remediation.
	PhaseRunning = "Running"

	// PhaseWaiting represents the state during remediation when the controller has done its job but still waiting for the result of the last remediation step.
	PhaseWaiting = "Waiting"

	// PhaseDeleting represents the state where host remediation has failed and the controller is deleting the unhealthy Machine object from the cluster.
	PhaseDeleting = "Deleting machine"

	// PhaseFailed represents the state where host will not be remediated.
	// Remediation Controller will set the state to PhaseFailed when a user has set bmh.Spec.Online to false.
	PhaseFailed = "Failed"
)

// Metal3RemediationSpec defines the desired state of Metal3Remediation.
type Metal3RemediationSpec struct {
	// Strategy field defines remediation strategy.
	Strategy *RemediationStrategy `json:"strategy,omitempty"`
}

// RemediationStrategy describes how to remediate machines.
type RemediationStrategy struct {
	// Type of remediation.
	Type RemediationType `json:"type,omitempty"`

	// Sets maximum number of remediation retries.
	// +optional
	RetryLimit int `json:"retryLimit,omitempty"`

	// Sets the timeout between remediation retries.
	// +optional
	Timeout *metav1.Duration `json:"timeout"`
}

// Metal3RemediationStatus defines the observed state of Metal3Remediation.
type Metal3RemediationStatus struct {
	// Phase represents the current phase of machine remediation.
	// E.g. Pending, Running, Done etc.
	// +optional
	Phase string `json:"phase,omitempty"`

	// RetryCount can be used as a counter during the remediation.
	// Field can hold number of reboots etc.
	// +optional
	RetryCount int `json:"retryCount,omitempty"`

	// LastRemediated identifies when the host was last remediated
	// +optional
	LastRemediated *metav1.Time `json:"lastRemediated,omitempty"`
}

// +kubebuilder:object:root=true

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:path=metal3remediations,scope=Namespaced,categories=cluster-api,shortName=m3r;m3remediation
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Retry limit",type=string,JSONPath=".spec.strategy.retryLimit",description="How many times remediation controller should attempt to remediate the host"
// +kubebuilder:printcolumn:name="Retry count",type=string,JSONPath=".status.retryCount",description="How many times remediation controller has tried to remediate the node"
// +kubebuilder:printcolumn:name="Last Remediated",type=string,JSONPath=".status.lastRemediated",description="Timestamp of the last remediation attempt"
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=".spec.strategy.type",description="Type of the remediation strategy"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase",description="Phase of the remediation"

// Metal3Remediation is the Schema for the metal3remediations API.
type Metal3Remediation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   Metal3RemediationSpec   `json:"spec,omitempty"`
	Status Metal3RemediationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// Metal3RemediationList contains a list of Metal3Remediation.
type Metal3RemediationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Metal3Remediation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Metal3Remediation{}, &Metal3RemediationList{})
}
