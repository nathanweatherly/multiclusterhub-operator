// Copyright Contributors to the Open Cluster Management project

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

package controllers

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"

	operatorv1 "github.com/stolostron/multiclusterhub-operator/api/v1"
	"github.com/stolostron/multiclusterhub-operator/pkg/channel"
	"github.com/stolostron/multiclusterhub-operator/pkg/deploying"
	"github.com/stolostron/multiclusterhub-operator/pkg/helmrepo"
	"github.com/stolostron/multiclusterhub-operator/pkg/imageoverrides"
	"github.com/stolostron/multiclusterhub-operator/pkg/manifest"
	"github.com/stolostron/multiclusterhub-operator/pkg/predicate"
	"github.com/stolostron/multiclusterhub-operator/pkg/subscription"
	utils "github.com/stolostron/multiclusterhub-operator/pkg/utils"
	"github.com/stolostron/multiclusterhub-operator/pkg/version"
	"sigs.k8s.io/yaml"

	appsubv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/v1"

	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/go-logr/logr"
)

// MultiClusterHubReconciler reconciles a MultiClusterHub object
type MultiClusterHubReconciler struct {
	Client    client.Client
	CacheSpec CacheSpec
	Scheme    *runtime.Scheme
	Log       logr.Logger
}

var resyncPeriod = time.Second * 20

const (
	crdPathEnvVar       = "CRDS_PATH"
	templatesPathEnvVar = "TEMPLATES_PATH"
	templatesKind       = "multiclusterhub"
	hubFinalizer        = "finalizer.operator.open-cluster-management.io"
)

//+kubebuilder:rbac:groups="";"admissionregistration.k8s.io";"apiextensions.k8s.io";"apiregistration.k8s.io";"apps";"apps.open-cluster-management.io";"authorization.k8s.io";"hive.openshift.io";"mcm.ibm.com";"proxy.open-cluster-management.io";"rbac.authorization.k8s.io";"security.openshift.io";"clusterview.open-cluster-management.io";"discovery.open-cluster-management.io";"wgpolicyk8s.io",resources=apiservices;channels;clusterjoinrequests;clusterrolebindings;clusterstatuses/log;configmaps;customresourcedefinitions;deployments;discoveryconfigs;hiveconfigs;mutatingwebhookconfigurations;validatingwebhookconfigurations;namespaces;pods;policyreports;replicasets;rolebindings;secrets;serviceaccounts;services;subjectaccessreviews;subscriptions;helmreleases;managedclusters;managedclustersets,verbs=get
//+kubebuilder:rbac:groups="";"admissionregistration.k8s.io";"apiextensions.k8s.io";"apiregistration.k8s.io";"apps";"apps.open-cluster-management.io";"authorization.k8s.io";"hive.openshift.io";"monitoring.coreos.com";"rbac.authorization.k8s.io";"mcm.ibm.com";"security.openshift.io",resources=apiservices;channels;clusterjoinrequests;clusterrolebindings;clusterroles;configmaps;customresourcedefinitions;deployments;hiveconfigs;mutatingwebhookconfigurations;validatingwebhookconfigurations;namespaces;rolebindings;secrets;serviceaccounts;services;servicemonitors;subjectaccessreviews;subscriptions;validatingwebhookconfigurations,verbs=create;update
//+kubebuilder:rbac:groups="";"apps";"apps.open-cluster-management.io";"admissionregistration.k8s.io";"apiregistration.k8s.io";"authorization.k8s.io";"config.openshift.io";"inventory.open-cluster-management.io";"mcm.ibm.com";"observability.open-cluster-management.io";"operator.open-cluster-management.io";"rbac.authorization.k8s.io";"hive.openshift.io";"clusterview.open-cluster-management.io";"discovery.open-cluster-management.io";"wgpolicyk8s.io",resources=apiservices;baremetalassets;clusterjoinrequests;configmaps;deployments;discoveryconfigs;helmreleases;ingresses;multiclusterhubs;multiclusterobservabilities;namespaces;hiveconfigs;rolebindings;servicemonitors;secrets;services;subjectaccessreviews;subscriptions;validatingwebhookconfigurations;pods;policyreports;managedclusters;managedclustersets,verbs=list
//+kubebuilder:rbac:groups="";"admissionregistration.k8s.io";"apiregistration.k8s.io";"apps";"authorization.k8s.io";"config.openshift.io";"mcm.ibm.com";"operator.open-cluster-management.io";"rbac.authorization.k8s.io";"storage.k8s.io";"apps.open-cluster-management.io";"hive.openshift.io";"clusterview.open-cluster-management.io";"wgpolicyk8s.io",resources=apiservices;helmreleases;hiveconfigs;configmaps;clusterjoinrequests;deployments;ingresses;multiclusterhubs;namespaces;rolebindings;secrets;services;subjectaccessreviews;validatingwebhookconfigurations;pods;policyreports;managedclusters;managedclustersets,verbs=watch;list
//+kubebuilder:rbac:groups="";"admissionregistration.k8s.io";"apps";"apps.open-cluster-management.io";"mcm.ibm.com";"monitoring.coreos.com";"operator.open-cluster-management.io";,resources=deployments;deployments/finalizers;helmreleases;services;services/finalizers;servicemonitors;servicemonitors/finalizers;validatingwebhookconfigurations;multiclusterhubs;multiclusterhubs/finalizers;multiclusterhubs/status,verbs=update
//+kubebuilder:rbac:groups="admissionregistration.k8s.io";"apiextensions.k8s.io";"apiregistration.k8s.io";"hive.openshift.io";"mcm.ibm.com";"rbac.authorization.k8s.io";,resources=apiservices;clusterroles;clusterrolebindings;customresourcedefinitions;hiveconfigs;mutatingwebhookconfigurations;validatingwebhookconfigurations,verbs=delete;deletecollection;list;watch;patch
//+kubebuilder:rbac:groups="";"apps";"apiregistration.k8s.io";"apps.open-cluster-management.io";"apiextensions.k8s.io";,resources=deployments;services;channels;customresourcedefinitions;apiservices,verbs=delete
//+kubebuilder:rbac:groups="";"action.open-cluster-management.io";"addon.open-cluster-management.io";"agent.open-cluster-management.io";"argoproj.io";"cluster.open-cluster-management.io";"work.open-cluster-management.io";"app.k8s.io";"apps.open-cluster-management.io";"authorization.k8s.io";"certificates.k8s.io";"clusterregistry.k8s.io";"config.openshift.io";"compliance.mcm.ibm.com";"hive.openshift.io";"hiveinternal.openshift.io";"internal.open-cluster-management.io";"inventory.open-cluster-management.io";"mcm.ibm.com";"multicloud.ibm.com";"policy.open-cluster-management.io";"proxy.open-cluster-management.io";"rbac.authorization.k8s.io";"view.open-cluster-management.io";"operator.open-cluster-management.io";"register.open-cluster-management.io";"coordination.k8s.io";"search.open-cluster-management.io";"submarineraddon.open-cluster-management.io";"discovery.open-cluster-management.io";"imageregistry.open-cluster-management.io",resources=applications;applications/status;applicationrelationships;applicationrelationships/status;baremetalassets;baremetalassets/status;baremetalassets/finalizers;certificatesigningrequests;certificatesigningrequests/approval;channels;channels/status;clustermanagementaddons;managedclusteractions;managedclusteractions/status;clusterdeployments;clusterpools;clusterclaims;discoveryconfigs;discoveredclusters;managedclusteraddons;managedclusteraddons/status;managedclusterinfos;managedclusterinfos/status;managedclustersets;managedclustersets/bind;managedclustersets/join;managedclustersets/status;managedclustersetbindings;managedclusters;managedclusters/accept;managedclusters/status;managedclusterviews;managedclusterviews/status;manifestworks;manifestworks/status;clustercurators;clustermanagers;clusterroles;clusterrolebindings;clusterstatuses/aggregator;clusterversions;compliances;configmaps;deployables;deployables/status;deployableoverrides;deployableoverrides/status;endpoints;endpointconfigs;events;helmrepos;helmrepos/status;klusterletaddonconfigs;machinepools;namespaces;placements;placementrules/status;placementdecisions;placementdecisions/status;placementrules;placementrules/status;pods;pods/log;policies;policies/status;placementbindings;policyautomations;policysets;policysets/status;roles;rolebindings;secrets;signers;subscriptions;subscriptions/status;subjectaccessreviews;submarinerconfigs;submarinerconfigs/status;syncsets;clustersyncs;leases;searchcustomizations;managedclusterimageregistries;managedclusterimageregistries/status,verbs=create;get;list;watch;update;delete;deletecollection;patch;approve;escalate;bind
//+kubebuilder:rbac:groups="operators.coreos.com",resources=subscriptions;clusterserviceversions;operatorgroups,verbs=create;get;list;patch;update;delete;watch
//+kubebuilder:rbac:groups="multicluster.openshift.io",resources=multiclusterengines,verbs=create;get;list;patch;update;delete;watch
//+kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,verbs=get;list;watch;update;patch

// AgentServiceConfig webhook delete check
//+kubebuilder:rbac:groups=agent-install.openshift.io,resources=agentserviceconfigs,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MultiClusterHub object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *MultiClusterHubReconciler) Reconcile(ctx context.Context, req ctrl.Request) (retQueue ctrl.Result, retError error) {
	r.Log = log.FromContext(ctx)

	r.Log.Info("Reconciling MultiClusterHub")

	// Fetch the MultiClusterHub instance
	multiClusterHub := &operatorv1.MultiClusterHub{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, multiClusterHub)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			r.Log.Info("MultiClusterHub resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		r.Log.Error(err, "Failed to get MultiClusterHub CR")
		return ctrl.Result{}, err
	}

	trackedNamespaces := utils.TrackedNamespaces(multiClusterHub)

	allDeploys, err := r.listDeployments(trackedNamespaces)
	if err != nil {
		return ctrl.Result{}, err
	}

	allHRs, err := r.listHelmReleases(trackedNamespaces)
	if err != nil {
		return ctrl.Result{}, err
	}

	allCRs, err := r.listCustomResources(multiClusterHub)
	if err != nil {
		return ctrl.Result{}, err
	}

	originalStatus := multiClusterHub.Status.DeepCopy()
	defer func() {
		statusQueue, statusError := r.syncHubStatus(multiClusterHub, originalStatus, allDeploys, allHRs, allCRs)
		if statusError != nil {
			r.Log.Error(retError, "Error updating status")
		}
		if empty := (reconcile.Result{}); retQueue == empty {
			retQueue = statusQueue
		}
		if retError == nil {
			retError = statusError
		}
	}()

	// Check if the multiClusterHub instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHubMarkedToBeDeleted := multiClusterHub.GetDeletionTimestamp() != nil
	if isHubMarkedToBeDeleted {
		terminating := NewHubCondition(operatorv1.Terminating, metav1.ConditionTrue, DeleteTimestampReason, "Multiclusterhub is being cleaned up.")
		SetHubCondition(&multiClusterHub.Status, *terminating)

		if contains(multiClusterHub.GetFinalizers(), hubFinalizer) {
			// Run finalization logic. If the finalization
			// logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			if err := r.finalizeHub(r.Log, multiClusterHub); err != nil {
				// Logging err and returning nil to ensure 45 second wait
				r.Log.Info(fmt.Sprintf("Finalizing: %s", err.Error()))
				return ctrl.Result{RequeueAfter: resyncPeriod}, nil
			}

			// Remove hubFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			multiClusterHub.SetFinalizers(remove(multiClusterHub.GetFinalizers(), hubFinalizer))

			err := r.Client.Update(context.TODO(), multiClusterHub)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !contains(multiClusterHub.GetFinalizers(), hubFinalizer) {
		if err := r.addFinalizer(r.Log, multiClusterHub); err != nil {
			return ctrl.Result{}, err
		}
	}

	var result ctrl.Result
	result, err = r.setDefaults(multiClusterHub)
	if result != (ctrl.Result{}) {
		return ctrl.Result{}, err
	}
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	// Read image overrides
	// First, attempt to read image overrides from environmental variables
	imageOverrides := imageoverrides.GetImageOverrides()
	if len(imageOverrides) == 0 {
		// If imageoverrides are not set from environmental variables, read from manifest
		r.Log.Info("Image Overrides not set from environment variables. Checking for overrides in manifest")
		imageOverrides, err = manifest.GetImageOverrides(multiClusterHub)
		if err != nil {
			r.Log.Error(err, "Could not get map of image overrides")
			return ctrl.Result{}, err
		}
	}

	// Select oauth proxy image to use. If OCP 4.8 use old version. If OCP 4.9+ use new version. Set with key oauth_proxy
	// before applying overrides.
	imageOverrides, err = r.overrideOauthImage(ctx, imageOverrides)
	if err != nil {
		return ctrl.Result{}, err
	}

	if imageRepo := utils.GetImageRepository(multiClusterHub); imageRepo != "" {
		r.Log.Info(fmt.Sprintf("Overriding Image Repository from annotation 'mch-imageRepository': %s", imageRepo))
		imageOverrides = utils.OverrideImageRepository(imageOverrides, imageRepo)
	}

	// Check for developer overrides
	if imageOverridesConfigmap := utils.GetImageOverridesConfigmap(multiClusterHub); imageOverridesConfigmap != "" {
		imageOverrides, err = r.OverrideImagesFromConfigmap(imageOverrides, multiClusterHub.GetNamespace(), imageOverridesConfigmap)
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("Could not find image override configmap: %s/%s", multiClusterHub.GetNamespace(), imageOverridesConfigmap))
			return ctrl.Result{}, err
		}
	}
	r.CacheSpec.ImageOverrides = imageOverrides
	r.CacheSpec.ManifestVersion = version.Version
	r.CacheSpec.ImageRepository = utils.GetImageRepository(multiClusterHub)
	r.CacheSpec.ImageOverridesCM = utils.GetImageOverridesConfigmap(multiClusterHub)

	err = r.maintainImageManifestConfigmap(multiClusterHub)
	if err != nil {
		r.Log.Error(err, "Error storing image manifests in configmap")
		return ctrl.Result{}, err
	}

	CustomUpgradeRequired, err := r.CustomSelfMgmtHubUpgradeRequired(multiClusterHub)
	if err != nil {
		r.Log.Error(err, "Error determining if upgrade specific logic is required")
		return ctrl.Result{}, err
	}

	if CustomUpgradeRequired {
		result, err = r.BeginEnsuringHubIsUpgradeable(multiClusterHub)
		if err != nil {
			r.Log.Info(fmt.Sprintf("Error starting to ensure local-cluster hub is upgradeable: %s", err.Error()))
			return ctrl.Result{RequeueAfter: resyncPeriod}, nil
		}
	}

	// Add installer labels to Helm-owned deployments
	myHelmReleases := getAppSubOwnedHelmReleases(allHRs, utils.GetAppsubs(multiClusterHub))
	myHRDeployments := getHelmReleaseOwnedDeployments(allDeploys, myHelmReleases)
	if err := r.labelDeployments(multiClusterHub, myHRDeployments); err != nil {
		return ctrl.Result{}, nil
	}

	// Do not reconcile objects if this instance of mch is labeled "paused"
	updatePausedCondition(multiClusterHub)
	if utils.IsPaused(multiClusterHub) {
		r.Log.Info("MultiClusterHub reconciliation is paused. Nothing more to do.")
		return ctrl.Result{}, nil
	}

	// 2.4->2.5 upgrade logic for cluster-backup
	matchCurrent, _ := regexp.MatchString("^2.4", multiClusterHub.Status.CurrentVersion)
	matchDesired, _ := regexp.MatchString("^2.5", multiClusterHub.Status.DesiredVersion)
	if (matchCurrent) && (matchDesired) {
		if multiClusterHub.Spec.EnableClusterBackup == true {
			blocking := NewHubCondition(operatorv1.Blocked, metav1.ConditionTrue, ResourceBlockReason, "When upgrading from version 2.4 to 2.5, cluster backup must be disabled")
			SetHubCondition(&multiClusterHub.Status, *blocking)
			return ctrl.Result{}, nil
		} else {
			res, err := r.ensureNoSubscription(multiClusterHub, subscription.OldClusterBackup(multiClusterHub))
			if res != (ctrl.Result{}) {
				return res, err
			}
			RemoveHubCondition(&multiClusterHub.Status, operatorv1.Blocked)
		}
	}

	result, err = r.ensureSubscriptionOperatorIsRunning(multiClusterHub, allDeploys)
	if result != (ctrl.Result{}) {
		return result, err
	}

	// Install CRDs
	var reason string
	reason, err = r.installCRDs(r.Log, multiClusterHub)
	if err != nil {
		condition := NewHubCondition(
			operatorv1.Progressing,
			metav1.ConditionFalse,
			reason,
			fmt.Sprintf("Error installing CRDs: %s", err),
		)
		SetHubCondition(&multiClusterHub.Status, *condition)
		return ctrl.Result{}, err
	}

	if utils.ProxyEnvVarsAreSet() {
		r.Log.Info(fmt.Sprintf("Proxy configuration environment variables are set. HTTP_PROXY: %s, HTTPS_PROXY: %s, NO_PROXY: %s", os.Getenv("HTTP_PROXY"), os.Getenv("HTTPS_PROXY"), os.Getenv("NO_PROXY")))
	}

	result, err = r.ensurePullSecretCreated(multiClusterHub, multiClusterHub.GetNamespace())
	if err != nil {
		condition := NewHubCondition(
			operatorv1.Progressing,
			metav1.ConditionFalse,
			err.Error(),
			fmt.Sprintf("Error fetching Pull Secret: %s", err),
		)
		SetHubCondition(&multiClusterHub.Status, *condition)
		return result, fmt.Errorf("failed to find pullsecret: %s", err)
	}

	if multiClusterHub.Enabled(operatorv1.Repo) {
		result, err = r.ensureDeployment(multiClusterHub, helmrepo.Deployment(multiClusterHub, r.CacheSpec.ImageOverrides))
		if result != (ctrl.Result{}) {
			return result, err
		}

		result, err = r.ensureService(multiClusterHub, helmrepo.Service(multiClusterHub))
		if result != (ctrl.Result{}) {
			return result, err
		}

		result, err = r.ensureChannel(multiClusterHub, channel.Channel(multiClusterHub))
		if result != (ctrl.Result{}) {
			return result, err
		}
	} else {
		result, err = r.ensureNoDeployment(multiClusterHub, helmrepo.Deployment(multiClusterHub, r.CacheSpec.ImageOverrides))
		if result != (ctrl.Result{}) {
			return result, err
		}

		result, err = r.ensureNoService(multiClusterHub, helmrepo.Service(multiClusterHub))
		if result != (ctrl.Result{}) {
			return result, err
		}

		result, err = r.ensureNoUnstructured(multiClusterHub, channel.Channel(multiClusterHub))
		if result != (ctrl.Result{}) {
			return result, err
		}
	}

	result, err = r.ensureMultiClusterEngine(multiClusterHub)
	if result != (ctrl.Result{}) {
		return result, err
	}

	result, err = r.ingressDomain(multiClusterHub)
	if result != (ctrl.Result{}) {
		return result, err
	}

	// Install CRDs
	reason, err = r.deployResources(r.Log, multiClusterHub)
	if err != nil {
		condition := NewHubCondition(
			operatorv1.Progressing,
			metav1.ConditionFalse,
			reason,
			fmt.Sprintf("Error deploying resources: %s", err),
		)
		SetHubCondition(&multiClusterHub.Status, *condition)
		return ctrl.Result{}, err
	}

	// Install the rest of the subscriptions in no particular order
	if multiClusterHub.Enabled(operatorv1.ManagementIngress) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.ManagementIngress(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.ManagementIngress(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}
	if multiClusterHub.Enabled(operatorv1.Console) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.Console(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.Console(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}
	if multiClusterHub.Enabled(operatorv1.Insights) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.Insights(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.Insights(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}
	if multiClusterHub.Enabled(operatorv1.GRC) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.GRC(multiClusterHub, r.CacheSpec.ImageOverrides))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.GRC(multiClusterHub, r.CacheSpec.ImageOverrides))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}
	if multiClusterHub.Enabled(operatorv1.ClusterLifecycle) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.ClusterLifecycle(multiClusterHub, r.CacheSpec.ImageOverrides))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.ClusterLifecycle(multiClusterHub, r.CacheSpec.ImageOverrides))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}
	if multiClusterHub.Enabled(operatorv1.Volsync) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.Volsync(multiClusterHub, r.CacheSpec.ImageOverrides))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.Volsync(multiClusterHub, r.CacheSpec.ImageOverrides))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}
	if multiClusterHub.Enabled(operatorv1.Search) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.Search(multiClusterHub, r.CacheSpec.ImageOverrides))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.Search(multiClusterHub, r.CacheSpec.ImageOverrides))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}
	if multiClusterHub.Enabled(operatorv1.ClusterBackup) {
		result, err = r.ensureNamespace(multiClusterHub, subscription.Namespace())
		if result != (ctrl.Result{}) {
			return result, err
		}
		result, err = r.ensureSubscription(multiClusterHub, subscription.ClusterBackup(multiClusterHub, r.CacheSpec.ImageOverrides))
		if result != (ctrl.Result{}) {
			return result, err
		}
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.ClusterBackup(multiClusterHub, r.CacheSpec.ImageOverrides))
		if result != (ctrl.Result{}) {
			return result, err
		}
		result, err = r.ensureNoNamespace(multiClusterHub, subscription.NamespaceUnstructured())
		if result != (ctrl.Result{}) {
			return result, err
		}
	}
	if multiClusterHub.Enabled(operatorv1.ClusterProxyAddon) {
		result, err = r.ensureSubscription(multiClusterHub, subscription.ClusterProxyAddon(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	} else {
		result, err = r.ensureNoSubscription(multiClusterHub, subscription.ClusterProxyAddon(multiClusterHub, r.CacheSpec.ImageOverrides, r.CacheSpec.IngressDomain))
	}
	if result != (ctrl.Result{}) {
		return result, err
	}

	if !utils.IsUnitTest() {
		if !multiClusterHub.Spec.DisableHubSelfManagement {
			result, err = r.ensureHubIsImported(multiClusterHub)
			if result != (ctrl.Result{}) {
				return result, err
			}
		} else {
			result, err = r.ensureHubIsExported(multiClusterHub)
			if result != (ctrl.Result{}) {
				return result, err
			}
		}
	}

	// Cleanup unused resources once components up-to-date
	if r.ComponentsAreRunning(multiClusterHub) {
		if r.pluginIsSupported(multiClusterHub) {
			result, err = r.addPluginToConsole(multiClusterHub)
			if result != (ctrl.Result{}) {
				return result, err
			}
		}
		result, err = r.ensureRemovalsGone(multiClusterHub)
		if result != (ctrl.Result{}) {
			return result, err
		}
	}

	return retQueue, retError
	// return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MultiClusterHubReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1.MultiClusterHub{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &operatorv1.MultiClusterHub{},
		}).
		Watches(&source.Kind{Type: &appsubv1.Subscription{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &operatorv1.MultiClusterHub{},
		}).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &operatorv1.MultiClusterHub{},
		}).
		Watches(&source.Kind{Type: &apiregistrationv1.APIService{}}, handler.Funcs{
			DeleteFunc: func(e event.DeleteEvent, q workqueue.RateLimitingInterface) {
				labels := e.Object.GetLabels()
				q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      labels["installer.name"],
					Namespace: labels["installer.namespace"],
				}})
			},
		}, builder.WithPredicates(predicate.DeletePredicate{})).
		Watches(&source.Kind{Type: &appsv1.Deployment{}},
			handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
				return []reconcile.Request{
					{NamespacedName: types.NamespacedName{
						Name:      a.GetLabels()["installer.name"],
						Namespace: a.GetLabels()["installer.namespace"],
					}},
				}
			}), builder.WithPredicates(predicate.InstallerLabelPredicate{})).
		Watches(&source.Kind{Type: &configv1.ClusterVersion{}},
			handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
				multiClusterHubList := &operatorv1.MultiClusterHubList{}
				if err := r.Client.List(context.TODO(), multiClusterHubList); err == nil && len(multiClusterHubList.Items) > 0 {
					mch := multiClusterHubList.Items[0]
					return []reconcile.Request{
						{NamespacedName: types.NamespacedName{
							Name:      mch.GetName(),
							Namespace: mch.GetNamespace(),
						}},
					}
				}
				return []reconcile.Request{}
			})).
		Complete(r)
}

// ingressDomain is discovered from Openshift cluster configuration resources
func (r *MultiClusterHubReconciler) ingressDomain(m *operatorv1.MultiClusterHub) (ctrl.Result, error) {
	if r.CacheSpec.IngressDomain != "" || utils.IsUnitTest() {
		return ctrl.Result{}, nil
	}
	ingress := &configv1.Ingress{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{
		Name: "cluster",
	}, ingress)
	// Don't fail on a unit test (Fake client won't find "cluster" Ingress)
	if err != nil {
		r.Log.Error(err, "Failed to get Ingress")
		return ctrl.Result{}, err
	}

	r.CacheSpec.IngressDomain = ingress.Spec.Domain
	return ctrl.Result{}, nil
}

func (r *MultiClusterHubReconciler) finalizeHub(reqLogger logr.Logger, m *operatorv1.MultiClusterHub) error {
	if r.pluginIsSupported(m) {
		if _, err := r.removePluginFromConsole(m); err != nil {
			return err
		}
	}
	if _, err := r.ensureHubIsExported(m); err != nil {
		return err
	}
	if err := r.cleanupAppSubscriptions(reqLogger, m); err != nil {
		return err
	}
	if err := r.cleanupNamespaces(reqLogger); err != nil {
		return err
	}
	if err := r.cleanupFoundation(reqLogger, m); err != nil {
		return err
	}
	if err := r.cleanupClusterRoles(reqLogger, m); err != nil {
		return err
	}
	if err := r.cleanupClusterRoleBindings(reqLogger, m); err != nil {
		return err
	}
	if err := r.cleanupMultiClusterEngine(reqLogger, m); err != nil {
		return err
	}
	if err := r.cleanupCRDs(reqLogger, m); err != nil {
		return err
	}
	if m.Spec.SeparateCertificateManagement {
		if err := r.cleanupPullSecret(reqLogger, m); err != nil {
			return err
		}
	}

	if err := r.orphanOwnedMultiClusterEngine(m); err != nil {
		return err
	}

	reqLogger.Info("Successfully finalized multiClusterHub")
	return nil
}

func (r *MultiClusterHubReconciler) addFinalizer(reqLogger logr.Logger, m *operatorv1.MultiClusterHub) error {
	reqLogger.Info("Adding Finalizer for the multiClusterHub")
	m.SetFinalizers(append(m.GetFinalizers(), hubFinalizer))

	// Update CR
	err := r.Client.Update(context.TODO(), m)
	if err != nil {
		reqLogger.Error(err, "Failed to update MultiClusterHub with finalizer")
		return err
	}
	return nil
}

func (r *MultiClusterHubReconciler) installCRDs(reqLogger logr.Logger, m *operatorv1.MultiClusterHub) (string, error) {
	crdDir, ok := os.LookupEnv(crdPathEnvVar)
	if !ok {
		err := fmt.Errorf("%s environment variable is required", crdPathEnvVar)
		reqLogger.Error(err, err.Error())
		return CRDRenderReason, err
	}

	files, err := os.ReadDir(crdDir)
	if err != nil {
		err := fmt.Errorf("unable to read CRD files from %s : %s", crdDir, err)
		reqLogger.Error(err, err.Error())
		return CRDRenderReason, err
	}

	crds := make([]*unstructured.Unstructured, 0, len(files))
	errs := make([]error, 0, len(files))
	for _, file := range files {
		fileName := file.Name()
		if filepath.Ext(fileName) != ".yaml" {
			continue
		}

		path := path.Join(crdDir, fileName)
		src, err := ioutil.ReadFile(filepath.Clean(path)) // #nosec G304 (filepath cleaned)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading file %s : %s", fileName, err))
			continue
		}

		crd := &unstructured.Unstructured{}
		if err = yaml.Unmarshal(src, crd); err != nil {
			errs = append(errs, fmt.Errorf("error unmarshalling file %s to unstructured: %s", fileName, err))
			continue
		}

		// Check that it is actually a CRD
		crdKind, _, err := unstructured.NestedString(crd.Object, "spec", "names", "kind")
		if err != nil {
			errs = append(errs, fmt.Errorf("error getting Kind field from %s: %s", fileName, err))
			continue
		}
		crdGroup, _, err := unstructured.NestedString(crd.Object, "spec", "group")
		if err != nil {
			errs = append(errs, fmt.Errorf("error getting Group field for %s : %s", fileName, err))
			continue
		}

		if crd.GetKind() != "CustomResourceDefinition" || crdKind == "" || crdGroup == "" {
			errs = append(errs, fmt.Errorf("error verifying file %s is a crd", fileName))
			continue
		}

		utils.AddInstallerLabel(crd, m.GetName(), m.GetNamespace())
		crds = append(crds, crd)
	}

	if len(errs) > 0 {
		message := mergeErrors(errs)
		err := fmt.Errorf("failed to render CRD templates: %s", message)
		reqLogger.Error(err, err.Error())
		return CRDRenderReason, err
	}

	for _, crd := range crds {
		err, ok := deploying.Deploy(r.Client, crd)
		if err != nil {
			err := fmt.Errorf("Failed to deploy %s %s", crd.GetKind(), crd.GetName())
			reqLogger.Error(err, err.Error())
			return DeployFailedReason, err
		}
		if ok {
			message := fmt.Sprintf("created new resource: %s %s", crd.GetKind(), crd.GetName())
			condition := NewHubCondition(operatorv1.Progressing, metav1.ConditionTrue, NewComponentReason, message)
			SetHubCondition(&m.Status, *condition)
		}
	}
	return "", nil
}

func (r *MultiClusterHubReconciler) deployResources(reqLogger logr.Logger, m *operatorv1.MultiClusterHub) (string, error) {
	resourceDir, ok := os.LookupEnv(templatesPathEnvVar)
	if !ok {
		err := fmt.Errorf("%s environment variable is required", templatesPathEnvVar)
		reqLogger.Error(err, err.Error())
		return ResourceRenderReason, err
	}

	resourceDir = path.Join(resourceDir, templatesKind, "base")
	files, err := os.ReadDir(resourceDir)
	if err != nil {
		err := fmt.Errorf("unable to read resource files from %s : %s", resourceDir, err)
		reqLogger.Error(err, err.Error())
		return ResourceRenderReason, err
	}

	resources := make([]*unstructured.Unstructured, 0, len(files))
	errs := make([]error, 0, len(files))
	for _, file := range files {
		fileName := file.Name()
		if filepath.Ext(fileName) != ".yaml" {
			continue
		}

		path := path.Join(resourceDir, fileName)
		src, err := ioutil.ReadFile(filepath.Clean(path)) // #nosec G304 (filepath cleaned)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading file %s : %s", fileName, err))
			continue
		}

		resource := &unstructured.Unstructured{}
		if err = yaml.Unmarshal(src, resource); err != nil {
			errs = append(errs, fmt.Errorf("error unmarshalling file %s to unstructured: %s", fileName, err))
			continue
		}

		resources = append(resources, resource)
	}

	if len(errs) > 0 {
		message := mergeErrors(errs)
		err := fmt.Errorf("failed to render resources: %s", message)
		reqLogger.Error(err, err.Error())
		return CRDRenderReason, err
	}

	for _, res := range resources {
		if res.GetNamespace() == m.Namespace {
			err := controllerutil.SetControllerReference(m, res, r.Scheme)
			if err != nil {
				r.Log.Error(
					err,
					fmt.Sprintf(
						"Failed to set controller reference on %s %s/%s",
						res.GetKind(), m.Namespace, res.GetName(),
					),
				)
			}
		}
		err, ok := deploying.Deploy(r.Client, res)
		if err != nil {
			err := fmt.Errorf("Failed to deploy %s %s", res.GetKind(), res.GetName())
			reqLogger.Error(err, err.Error())
			return DeployFailedReason, err
		}
		if ok {
			message := fmt.Sprintf("created new resource: %s %s", res.GetKind(), res.GetName())
			condition := NewHubCondition(operatorv1.Progressing, metav1.ConditionTrue, NewComponentReason, message)
			SetHubCondition(&m.Status, *condition)
		}
	}

	return "", nil
}

func updatePausedCondition(m *operatorv1.MultiClusterHub) {
	c := GetHubCondition(m.Status, operatorv1.Progressing)

	if utils.IsPaused(m) {
		// Pause condition needs to go on
		if c == nil || c.Reason != PausedReason {
			condition := NewHubCondition(operatorv1.Progressing, metav1.ConditionUnknown, PausedReason, "Multiclusterhub is paused")
			SetHubCondition(&m.Status, *condition)
		}
	} else {
		// Pause condition needs to come off
		if c != nil && c.Reason == PausedReason {
			condition := NewHubCondition(operatorv1.Progressing, metav1.ConditionTrue, ResumedReason, "Multiclusterhub is resumed")
			SetHubCondition(&m.Status, *condition)
		}

	}
}

func (r *MultiClusterHubReconciler) setDefaults(m *operatorv1.MultiClusterHub) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log

	updateNecessary := false

	if utils.SetDefaultComponents(m) {
		updateNecessary = true
	}

	if utils.DeduplicateComponents(m) {
		updateNecessary = true
	}

	if utils.MigrateToggles(m) {
		updateNecessary = true

	}

	if utils.MchIsValid(m) && os.Getenv("ACM_HUB_OCP_VERSION") != "" && !updateNecessary {
		return ctrl.Result{}, nil
	}
	log.Info("MultiClusterHub is Invalid. Updating with proper defaults")

	if len(m.Spec.Ingress.SSLCiphers) == 0 {
		m.Spec.Ingress.SSLCiphers = utils.DefaultSSLCiphers
		updateNecessary = true
	}

	if !utils.AvailabilityConfigIsValid(m.Spec.AvailabilityConfig) {
		m.Spec.AvailabilityConfig = operatorv1.HAHigh
		updateNecessary = true
	}

	// If OCP 4.10+ then set then enable the MCE console. Else ensure it is disabled
	clusterVersion := &configv1.ClusterVersion{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: "version"}, clusterVersion)
	if err != nil {
		log.Error(err, "Failed to detect clusterversion")
		return ctrl.Result{}, err
	}
	currentClusterVersion := ""
	if len(clusterVersion.Status.History) == 0 {
		if !utils.IsUnitTest() {
			log.Error(err, "Failed to detect status in clusterversion.status.history")
			return ctrl.Result{}, err
		}
	}

	if utils.IsUnitTest() {
		// If unit test pass along a version, Can't set status in unit test
		currentClusterVersion = "4.9.0"
	} else {
		currentClusterVersion = clusterVersion.Status.History[0].Version
	}

	// Set OCP version as env var, so that charts can render this value
	err = os.Setenv("ACM_HUB_OCP_VERSION", currentClusterVersion)
	if err != nil {
		log.Error(err, "Failed to set ACM_HUB_OCP_VERSION environment variable")
		return ctrl.Result{}, err
	}

	if updateNecessary {
		// Apply defaults to server
		err = r.Client.Update(ctx, m)
		if err != nil {
			r.Log.Error(err, "Failed to update MultiClusterHub", "MultiClusterHub.Namespace", m.Namespace, "MultiClusterHub.Name", m.Name)
			return ctrl.Result{}, err
		}
		r.Log.Info("MultiClusterHub successfully updated")
		return ctrl.Result{Requeue: true}, nil

	}
	log.Info("No updates to defaults detected")
	return ctrl.Result{}, nil

}
