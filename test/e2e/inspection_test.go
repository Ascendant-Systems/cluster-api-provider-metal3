package e2e

import (
	bmov1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	inspectAnnotation = "inspect.metal3.io"
)

func inspection() {
	Logf("Starting inspection tests")

	var (
		numberOfAvailableBMHs = 2 * numberOfWorkers
	)

	bootstrapClient := bootstrapClusterProxy.GetClient()

	Logf("Request inspection for all Available BMHs via API")
	availableBMHList := bmov1alpha1.BareMetalHostList{}
	Expect(bootstrapClient.List(ctx, &availableBMHList, client.InNamespace(namespace))).To(Succeed())
	Logf("Request inspection for all Available BMHs via API")
	for _, bmh := range availableBMHList.Items {
		if bmh.Status.Provisioning.State == bmov1alpha1.StateAvailable {
			AnnotateBmh(ctx, bootstrapClient, bmh, inspectAnnotation, pointer.String(""))
		}
	}

	WaitForNumBmhInState(ctx, bmov1alpha1.StateInspecting, WaitForNumInput{
		Client:    bootstrapClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfAvailableBMHs,
		Intervals: e2eConfig.GetIntervals(specName, "wait-bmh-inspecting"),
	})

	WaitForNumBmhInState(ctx, bmov1alpha1.StateAvailable, WaitForNumInput{
		Client:    bootstrapClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfAvailableBMHs,
		Intervals: e2eConfig.GetIntervals(specName, "wait-bmh-available"),
	})

	By("INSPECTION TESTS PASSED!")
}
