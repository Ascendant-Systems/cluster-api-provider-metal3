package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"time"

	bmov1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	infrav1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func nodeReuse(clusterClient client.Client) {
	Logf("Starting node reuse tests")
	var (
		targetClusterClient = targetCluster.GetClient()
		clientSet           = targetCluster.GetClientSet()
		kubernetesVersion   = e2eConfig.GetVariable("KUBERNETES_VERSION")
		upgradedK8sVersion  = e2eConfig.GetVariable("UPGRADED_K8S_VERSION")
		controlplaneTaints  = []corev1.Taint{{Key: "node-role.kubernetes.io/control-plane", Effect: corev1.TaintEffectNoSchedule},
			{Key: "node-role.kubernetes.io/master", Effect: corev1.TaintEffectNoSchedule}}
		imageNamePrefix string
	)

	const (
		artifactoryURL = "https://artifactory.nordix.org/artifactory/metal3/images/k8s"
		imagesURL      = "http://172.22.0.1/images"
		ironicImageDir = "/opt/metal3-dev-env/ironic/html/images"
		nodeReuseLabel = "infrastructure.cluster.x-k8s.io/node-reuse"
	)

	Logf("KUBERNETES VERSION: %v", kubernetesVersion)
	Logf("UPGRADED K8S VERSION: %v", upgradedK8sVersion)
	Logf("NUMBER OF CONTROLPLANE BMH: %v", numberOfControlplane)
	Logf("NUMBER OF WORKER BMH: %v", numberOfWorkers)

	ListBareMetalHosts(ctx, clusterClient, client.InNamespace(namespace))
	ListMetal3Machines(ctx, clusterClient, client.InNamespace(namespace))
	ListMachines(ctx, clusterClient, client.InNamespace(namespace))
	ListNodes(ctx, targetClusterClient)

	By("Untaint all CP nodes before scaling down machinedeployment")
	controlplaneNodes := getControlplaneNodes(clientSet)
	untaintNodes(targetClusterClient, controlplaneNodes, controlplaneTaints)

	By("Scale down MachineDeployment to 0")
	ScaleMachineDeployment(ctx, clusterClient, clusterName, namespace, 0)

	Byf("Wait until the worker is scaled down and %d BMH(s) Available", numberOfWorkers)
	WaitForNumBmhInState(ctx, bmov1alpha1.StateAvailable, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfWorkers,
		Intervals: e2eConfig.GetIntervals(specName, "wait-bmh-available"),
	})

	By("Get the provisioned BMH names and UUIDs")
	kcpBmhBeforeUpgrade := getProvisionedBmhNamesUuids(clusterClient)

	By("Download image")
	osType := strings.ToLower(os.Getenv("OS"))
	Expect(osType).ToNot(Equal(""))
	if osType != "centos" {
		imageNamePrefix = "UBUNTU_22.04_NODE_IMAGE_K8S"
	} else {
		imageNamePrefix = "CENTOS_9_NODE_IMAGE_K8S"
	}
	imageName := fmt.Sprintf("%s_%s.qcow2", imageNamePrefix, upgradedK8sVersion)
	Logf("IMAGE_NAME: %v", imageName)
	rawImageName := fmt.Sprintf("%s_%s-raw.img", imageNamePrefix, upgradedK8sVersion)
	Logf("RAW_IMAGE_NAME: %v", rawImageName)
	imageLocation := fmt.Sprintf("%s_%s/", artifactoryURL, upgradedK8sVersion)
	Logf("IMAGE_LOCATION: %v", imageLocation)
	imageURL := fmt.Sprintf("%s/%s", imagesURL, rawImageName)
	Logf("IMAGE_URL: %v", imageURL)
	imageChecksum := fmt.Sprintf("%s/%s.md5sum", imagesURL, rawImageName)
	Logf("IMAGE_CHECKSUM: %v", imageChecksum)

	// Check if node image with upgraded k8s version exist, if not download it
	if _, err := os.Stat(fmt.Sprintf("%s/%s", ironicImageDir, rawImageName)); err == nil {
		Logf("Local image %v is found", rawImageName)
	} else if os.IsNotExist(err) {
		Logf("Local image %v/%v is not found", ironicImageDir, rawImageName)
		err = DownloadFile(fmt.Sprintf("%s/%s", ironicImageDir, imageName), fmt.Sprintf("%s/%s", imageLocation, imageName))
		Expect(err).To(BeNil())
		cmd := exec.Command("qemu-img", "convert", "-O", "raw", fmt.Sprintf("%s/%s", ironicImageDir, imageName), fmt.Sprintf("%s/%s", ironicImageDir, rawImageName))
		err = cmd.Run()
		Expect(err).To(BeNil())
		cmd = exec.Command("md5sum", fmt.Sprintf("%s/%s", ironicImageDir, rawImageName))
		output, err := cmd.CombinedOutput()
		Expect(err).To(BeNil())
		md5sum := strings.Fields(string(output))[0]
		err = os.WriteFile(fmt.Sprintf("%s/%s.md5sum", ironicImageDir, rawImageName), []byte(md5sum), 0777)
		Expect(err).To(BeNil())
	} else {
		fmt.Fprintf(GinkgoWriter, "ERROR: %v\n", err)
		os.Exit(1)
	}
	By("Update KCP Metal3MachineTemplate with upgraded image to boot and set nodeReuse field to 'True'")
	m3machineTemplateName := fmt.Sprintf("%s-controlplane", clusterName)
	updateNodeReuse(true, m3machineTemplateName, clusterClient)
	updateBootImage(m3machineTemplateName, clusterClient, imageURL, imageChecksum, "raw", "md5")

	Byf("Update KCP to upgrade k8s version and binaries from %s to %s", kubernetesVersion, upgradedK8sVersion)
	kcpObj := framework.GetKubeadmControlPlaneByCluster(ctx, framework.GetKubeadmControlPlaneByClusterInput{
		Lister:      clusterClient,
		ClusterName: clusterName,
		Namespace:   namespace,
	})
	patch := []byte(fmt.Sprintf(`{
		"spec": {
			"rolloutStrategy": {
				"rollingUpdate": {
					"maxSurge": 0
				}
			},
			"version": "%s"
		}
	}`, upgradedK8sVersion))
	err := clusterClient.Patch(ctx, kcpObj, client.RawPatch(types.MergePatchType, patch))
	Expect(err).To(BeNil(), "Failed to patch KubeadmControlPlane")

	By("Check if only a single machine is in Deleting state and no other new machines are in Provisioning state")
	WaitForNumMachinesInState(ctx, clusterv1.MachinePhaseDeleting, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  1,
		Intervals: e2eConfig.GetIntervals(specName, "wait-machine-deleting"),
	})
	// Since we do scale in, no Machine should start provisioning yet (the old must be deleted first)
	machineList := &clusterv1.MachineList{}
	Expect(clusterClient.List(ctx, machineList, client.InNamespace(namespace))).To(Succeed())
	Expect(FilterMachinesByPhase(machineList.Items, clusterv1.MachinePhaseProvisioning)).To(HaveLen(0))

	Byf("Wait until 1 BMH is in deprovisioning state")
	WaitForNumBmhInState(ctx, bmov1alpha1.StateDeprovisioning, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  1,
		Intervals: e2eConfig.GetIntervals(specName, "wait-bmh-deprovisioning"),
	})

	Logf("Find the deprovisioning BMH")
	bmhList := bmov1alpha1.BareMetalHostList{}
	Expect(clusterClient.List(ctx, &bmhList, client.InNamespace(namespace))).To(Succeed())
	deprovisioningBmhs := FilterBmhsByProvisioningState(bmhList.Items, bmov1alpha1.StateDeprovisioning)
	Expect(deprovisioningBmhs).To(HaveLen(1))
	key := types.NamespacedName{Name: deprovisioningBmhs[0].Name, Namespace: namespace}

	By("Wait until above deprovisioning BMH is in available state again")
	Eventually(
		func(g Gomega) {
			bmh := bmov1alpha1.BareMetalHost{}
			g.Expect(clusterClient.Get(ctx, key, &bmh)).To(Succeed())
			g.Expect(bmh.Status.Provisioning.State).To(Equal(bmov1alpha1.StateAvailable))
		}, e2eConfig.GetIntervals(specName, "wait-bmh-deprovisioning-available")...,
	).Should(Succeed())

	By("Check if just deprovisioned BMH re-used for the next provisioning")
	Eventually(
		func(g Gomega) {
			bmh := bmov1alpha1.BareMetalHost{}
			g.Expect(clusterClient.Get(ctx, key, &bmh)).To(Succeed())
			g.Expect(bmh.Status.Provisioning.State).To(Equal(bmov1alpha1.StateProvisioning))
		}, e2eConfig.GetIntervals(specName, "wait-bmh-available-provisioning")...,
	).Should(Succeed())

	Byf("Wait until two machines become running and updated with the new %s k8s version", upgradedK8sVersion)
	runningAndUpgraded := func(machine clusterv1.Machine) bool {
		running := machine.Status.GetTypedPhase() == clusterv1.MachinePhaseRunning
		upgraded := *machine.Spec.Version == upgradedK8sVersion
		return (running && upgraded)
	}
	WaitForNumMachines(ctx, runningAndUpgraded, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  2,
		Intervals: e2eConfig.GetIntervals(specName, "wait-machine-running"),
	})

	ListBareMetalHosts(ctx, clusterClient, client.InNamespace(namespace))
	ListMetal3Machines(ctx, clusterClient, client.InNamespace(namespace))
	ListMachines(ctx, clusterClient, client.InNamespace(namespace))
	ListNodes(ctx, targetClusterClient)

	By("Untaint CP nodes after upgrade of two controlplane nodes")
	controlplaneNodes = getControlplaneNodes(clientSet)
	untaintNodes(targetClusterClient, controlplaneNodes, controlplaneTaints)

	Byf("Wait until all %v KCP machines become running and updated with new %s k8s version", numberOfControlplane, upgradedK8sVersion)
	WaitForNumMachines(ctx, runningAndUpgraded, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfControlplane,
		Intervals: e2eConfig.GetIntervals(specName, "wait-machine-running"),
	})

	By("Get the provisioned BMH names and UUIDs after upgrade")
	kcpBmhAfterUpgrade := getProvisionedBmhNamesUuids(clusterClient)

	By("Check difference between before and after upgrade mappings")
	equal := reflect.DeepEqual(kcpBmhBeforeUpgrade, kcpBmhAfterUpgrade)
	Expect(equal).To(BeTrue(), "The same BMHs were not reused in KubeadmControlPlane test case")

	By("Put maxSurge field in KubeadmControlPlane back to default value(1)")
	ctrlplane := controlplanev1.KubeadmControlPlane{}
	Expect(clusterClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: clusterName}, &ctrlplane)).To(Succeed())
	patch = []byte(`{
		"spec": {
			"rolloutStrategy": {
				"rollingUpdate": {
					"maxSurge": 1
				}
			}
		}
	}`)
	// Retry if failed to patch
	for retry := 0; retry < 3; retry++ {
		err = clusterClient.Patch(ctx, &ctrlplane, client.RawPatch(types.MergePatchType, patch))
		if err == nil {
			break
		}
		time.Sleep(30 * time.Second)
	}

	By("Untaint all CP nodes")
	// The rest of CP nodes may take time to be untaintable
	// We have untainted the 2 first CPs
	for untaintedNodeCount := 0; untaintedNodeCount < numberOfControlplane-2; {
		controlplaneNodes = getControlplaneNodes(clientSet)
		untaintedNodeCount = untaintNodes(targetClusterClient, controlplaneNodes, controlplaneTaints)
		time.Sleep(10 * time.Second)
	}

	By("Scale the controlplane down to 1")
	ScaleKubeadmControlPlane(ctx, clusterClient, client.ObjectKey{Namespace: namespace, Name: clusterName}, 1)

	Byf("Wait until controlplane is scaled down and %d BMHs are Available", numberOfControlplane)
	WaitForNumBmhInState(ctx, bmov1alpha1.StateAvailable, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfControlplane,
		Intervals: e2eConfig.GetIntervals(specName, "wait-cp-available"),
	})

	ListBareMetalHosts(ctx, clusterClient, client.InNamespace(namespace))
	ListMetal3Machines(ctx, clusterClient, client.InNamespace(namespace))
	ListMachines(ctx, clusterClient, client.InNamespace(namespace))
	ListNodes(ctx, targetClusterClient)

	By("Get MachineDeployment")
	machineDeployments := framework.GetMachineDeploymentsByCluster(ctx, framework.GetMachineDeploymentsByClusterInput{
		Lister:      clusterClient,
		ClusterName: clusterName,
		Namespace:   namespace,
	})
	Expect(len(machineDeployments)).To(Equal(1), "Expected exactly 1 MachineDeployment")
	machineDeploy := machineDeployments[0]

	By("Get Metal3MachineTemplate name for MachineDeployment")
	m3machineTemplateName = fmt.Sprintf("%s-workers", clusterName)

	By("Point to proper Metal3MachineTemplate in MachineDeployment")
	pointMDtoM3mt(m3machineTemplateName, machineDeploy.Name, clusterClient)

	By("Scale the worker up to 1 to start testing MachineDeployment")
	ScaleMachineDeployment(ctx, clusterClient, clusterName, namespace, 1)

	Byf("Wait until the worker BMH becomes provisioned")
	WaitForNumBmhInState(ctx, bmov1alpha1.StateProvisioned, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  2,
		Intervals: e2eConfig.GetIntervals(specName, "wait-bmh-provisioned"),
	})

	Byf("Wait until the worker machine becomes running")
	WaitForNumMachinesInState(ctx, clusterv1.MachinePhaseRunning, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  2,
		Intervals: e2eConfig.GetIntervals(specName, "wait-machine-running"),
	})

	By("Get the provisioned BMH names and UUIDs before starting upgrade in MachineDeployment")
	mdBmhBeforeUpgrade := getProvisionedBmhNamesUuids(clusterClient)

	By("List all available BMHs, remove nodeReuse label from them if any")
	bmhs := bmov1alpha1.BareMetalHostList{}
	Expect(clusterClient.List(ctx, &bmhs, client.InNamespace(namespace))).To(Succeed())
	for _, item := range bmhs.Items {
		if item.Status.Provisioning.State == bmov1alpha1.StateAvailable {
			// We make sure that all available BMHs are choosable by removing nodeReuse label
			// set on them while testing KCP node reuse scenario previously.
			DeleteNodeReuseLabelFromHost(ctx, clusterClient, item, nodeReuseLabel)
		}
	}

	By("Update MD Metal3MachineTemplate with upgraded image to boot and set nodeReuse field to 'True'")
	updateNodeReuse(true, m3machineTemplateName, clusterClient)
	updateBootImage(m3machineTemplateName, clusterClient, imageURL, imageChecksum, "raw", "md5")

	Byf("Update MD to upgrade k8s version and binaries from %s to %s", kubernetesVersion, upgradedK8sVersion)
	// Note: We have only 4 nodes (3 control-plane and 1 worker) so we
	// must allow maxUnavailable 1 here or it will get stuck.
	patch = []byte(fmt.Sprintf(`{
		"spec": {
			"strategy": {
				"rollingUpdate": {
					"maxSurge": 0,
					"maxUnavailable": 1
				}
			},
			"template": {
				"spec": {
					"version": "%s"
				}
			}
		}
	}`, upgradedK8sVersion))

	err = clusterClient.Patch(ctx, machineDeploy, client.RawPatch(types.MergePatchType, patch))
	Expect(err).To(BeNil(), "Failed to patch MachineDeployment")

	Byf("Wait until %d BMH(s) in deprovisioning state", numberOfWorkers)
	WaitForNumBmhInState(ctx, bmov1alpha1.StateDeprovisioning, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfWorkers,
		Intervals: e2eConfig.GetIntervals(specName, "wait-bmh-deprovisioning"),
	})

	Logf("Find the deprovisioning BMH")
	bmhList = bmov1alpha1.BareMetalHostList{}
	Expect(clusterClient.List(ctx, &bmhList, client.InNamespace(namespace))).To(Succeed())
	deprovisioningBmhs = FilterBmhsByProvisioningState(bmhList.Items, bmov1alpha1.StateDeprovisioning)
	Expect(deprovisioningBmhs).To(HaveLen(1))
	key = types.NamespacedName{Name: deprovisioningBmhs[0].Name, Namespace: namespace}

	By("Wait until the above deprovisioning BMH is in available state again")
	Eventually(
		func(g Gomega) {
			bmh := bmov1alpha1.BareMetalHost{}
			g.Expect(clusterClient.Get(ctx, key, &bmh)).To(Succeed())
			g.Expect(bmh.Status.Provisioning.State).To(Equal(bmov1alpha1.StateAvailable))
		},
		e2eConfig.GetIntervals(specName, "wait-bmh-deprovisioning-available")...,
	).Should(Succeed())

	By("Check if just deprovisioned BMH re-used for next provisioning")
	Eventually(
		func(g Gomega) {
			bmh := bmov1alpha1.BareMetalHost{}
			key := types.NamespacedName{Name: deprovisioningBmhs[0].Name, Namespace: namespace}
			g.Expect(clusterClient.Get(ctx, key, &bmh)).To(Succeed())
			g.Expect(bmh.Status.Provisioning.State).To(Equal(bmov1alpha1.StateProvisioning))
		},
		e2eConfig.GetIntervals(specName, "wait-bmh-available-provisioning")...,
	).Should(Succeed())

	Byf("Wait until worker machine becomes running and updated with new %s k8s version", upgradedK8sVersion)
	WaitForNumMachines(ctx, runningAndUpgraded, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  2,
		Intervals: e2eConfig.GetIntervals(specName, "wait-machine-running"),
	})

	By("Get provisioned BMH names and UUIDs after upgrade in MachineDeployment")
	mdBmhAfterUpgrade := getProvisionedBmhNamesUuids(clusterClient)

	By("Check difference between before and after upgrade mappings in MachineDeployment")
	equal = reflect.DeepEqual(mdBmhBeforeUpgrade, mdBmhAfterUpgrade)
	Expect(equal).To(BeTrue(), "The same BMHs were not reused in MachineDeployment")

	ListBareMetalHosts(ctx, clusterClient, client.InNamespace(namespace))
	ListMetal3Machines(ctx, clusterClient, client.InNamespace(namespace))
	ListMachines(ctx, clusterClient, client.InNamespace(namespace))
	ListNodes(ctx, targetClusterClient)

	By("Scale controlplane up to 3")
	ScaleKubeadmControlPlane(ctx, clusterClient, client.ObjectKey{Namespace: namespace, Name: clusterName}, 3)
	WaitForNumBmhInState(ctx, bmov1alpha1.StateProvisioned, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfAllBmh,
		Intervals: e2eConfig.GetIntervals(specName, "wait-bmh-provisioned"),
	})

	Byf("Wait until all %d machine(s) become(s) running", numberOfAllBmh)
	WaitForNumMachinesInState(ctx, clusterv1.MachinePhaseRunning, WaitForNumInput{
		Client:    clusterClient,
		Options:   []client.ListOption{client.InNamespace(namespace)},
		Replicas:  numberOfAllBmh,
		Intervals: e2eConfig.GetIntervals(specName, "wait-machine-running"),
	})

	By("NODE REUSE TESTS PASSED!")
}

func getControlplaneNodes(clientSet *kubernetes.Clientset) *corev1.NodeList {
	controlplaneNodesRequirement, err := labels.NewRequirement("node-role.kubernetes.io/control-plane", selection.Exists, []string{})
	Expect(err).To(BeNil(), "Failed to set up worker Node requirements")
	controlplaneNodesSelector := labels.NewSelector().Add(*controlplaneNodesRequirement)
	controlplaneListOptions = metav1.ListOptions{LabelSelector: controlplaneNodesSelector.String()}
	controlplaneNodes, err := clientSet.CoreV1().Nodes().List(ctx, controlplaneListOptions)
	Expect(err).To(BeNil(), "Failed to get controlplane nodes")
	Logf("controlplaneNodes found %v", len(controlplaneNodes.Items))
	return controlplaneNodes
}

func getProvisionedBmhNamesUuids(clusterClient client.Client) []string {
	bmhs := bmov1alpha1.BareMetalHostList{}
	var nameUUIDList []string
	Expect(clusterClient.List(ctx, &bmhs, client.InNamespace(namespace))).To(Succeed())
	for _, item := range bmhs.Items {
		if item.WasProvisioned() {
			concat := "metal3/" + item.Name + "=metal3://" + (string)(item.UID)
			nameUUIDList = append(nameUUIDList, concat)
		}
	}
	return nameUUIDList
}

func updateNodeReuse(nodeReuse bool, m3machineTemplateName string, clusterClient client.Client) {
	m3machineTemplate := infrav1.Metal3MachineTemplate{}
	Expect(clusterClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: m3machineTemplateName}, &m3machineTemplate)).To(Succeed())
	helper, err := patch.NewHelper(&m3machineTemplate, clusterClient)
	Expect(err).NotTo(HaveOccurred())
	m3machineTemplate.Spec.NodeReuse = nodeReuse
	Expect(helper.Patch(ctx, &m3machineTemplate)).To(Succeed())

	// verify that nodeReuse field is updated
	Expect(clusterClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: m3machineTemplateName}, &m3machineTemplate)).To(Succeed())
	Expect(m3machineTemplate.Spec.NodeReuse).To(BeEquivalentTo(nodeReuse))
}

func pointMDtoM3mt(m3mtname, mdName string, clusterClient client.Client) {
	md := clusterv1.MachineDeployment{}
	Expect(clusterClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: mdName}, &md)).To(Succeed())
	helper, err := patch.NewHelper(&md, clusterClient)
	Expect(err).NotTo(HaveOccurred())
	md.Spec.Template.Spec.InfrastructureRef.Name = m3mtname
	Expect(helper.Patch(ctx, &md)).To(Succeed())

	// verify that MachineDeployment is pointing to exact m3mt where nodeReuse is set to 'True'
	Expect(clusterClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: mdName}, &md)).To(Succeed())
	Expect(md.Spec.Template.Spec.InfrastructureRef.Name).To(BeEquivalentTo(fmt.Sprintf("%s-workers", clusterName)))
}

func updateBootImage(m3machineTemplateName string, clusterClient client.Client, imageURL string, imageChecksum string, checksumType string, imageFormat string) {
	m3machineTemplate := infrav1.Metal3MachineTemplate{}
	Expect(clusterClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: m3machineTemplateName}, &m3machineTemplate)).To(Succeed())
	helper, err := patch.NewHelper(&m3machineTemplate, clusterClient)
	Expect(err).NotTo(HaveOccurred())
	m3machineTemplate.Spec.Template.Spec.Image.URL = imageURL
	m3machineTemplate.Spec.Template.Spec.Image.Checksum = imageChecksum
	m3machineTemplate.Spec.Template.Spec.Image.DiskFormat = &checksumType
	m3machineTemplate.Spec.Template.Spec.Image.ChecksumType = &imageFormat
	Expect(helper.Patch(ctx, &m3machineTemplate)).To(Succeed())
}

func untaintNodes(targetClusterClient client.Client, nodes *corev1.NodeList, taints []corev1.Taint) (count int) {
	count = 0
	for i := range nodes.Items {
		Logf("Untainting node %v ...", nodes.Items[i].Name)
		newNode, changed := removeTaint(&nodes.Items[i], taints)
		if changed {
			patchHelper, err := patch.NewHelper(&nodes.Items[i], targetClusterClient)
			Expect(err).To(BeNil())
			Expect(patchHelper.Patch(ctx, newNode)).To(Succeed(), "Failed to patch node")
			count++
		}
	}
	return
}

func removeTaint(node *corev1.Node, taints []corev1.Taint) (*corev1.Node, bool) {
	newNode := node.DeepCopy()
	nodeTaints := newNode.Spec.Taints
	if len(nodeTaints) == 0 {
		return newNode, false
	}

	if !taintExists(nodeTaints, taints) {
		return newNode, false
	}

	newTaints, _ := deleteTaint(nodeTaints, taints)
	newNode.Spec.Taints = newTaints
	return newNode, true
}

func taintExists(taints []corev1.Taint, taintsToFind []corev1.Taint) bool {
	for _, taint := range taints {
		for _, taintToFind := range taintsToFind {
			if taint.MatchTaint(&taintToFind) {
				return true
			}
		}
	}
	return false
}

func deleteTaint(taints []corev1.Taint, taintsToDelete []corev1.Taint) ([]corev1.Taint, bool) {
	newTaints := []corev1.Taint{}
	deleted := false
	for i := range taints {
		currentTaintDeleted := false
		for _, taintToDelete := range taintsToDelete {
			if taintToDelete.MatchTaint(&taints[i]) {
				deleted = true
				currentTaintDeleted = true
			}
		}
		if !currentTaintDeleted {
			newTaints = append(newTaints, taints[i])
		}
	}
	return newTaints, deleted
}
