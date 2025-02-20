// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package subscription

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	operatorsv1 "github.com/stolostron/multiclusterhub-operator/api/v1"
	"github.com/stolostron/multiclusterhub-operator/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func TestValidate(t *testing.T) {
	mch := &operatorsv1.MultiClusterHub{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test"},
		Spec: operatorsv1.MultiClusterHubSpec{
			ImagePullSecret:   "test",
			CustomCAConfigmap: "test-config",
		},
	}
	ovr := map[string]string{}

	// 1. Valid mch
	sub := ClusterLifecycle(mch, ovr)

	// 2. Modified ImagePullSecret
	mch1 := mch.DeepCopy()
	mch1.Spec.ImagePullSecret = "notTest"
	sub1 := ClusterLifecycle(mch1, ovr)

	// 3. Modified ImagePullPolicy
	mch2 := mch.DeepCopy()
	mch2.Spec.Overrides = &operatorsv1.Overrides{
		ImagePullPolicy: corev1.PullNever,
	}
	sub2 := ClusterLifecycle(mch2, ovr)

	// 4. Modified ImageRepository
	mch3 := mch.DeepCopy()
	mch3.SetAnnotations(map[string]string{utils.AnnotationImageRepo: "notquay.io/closed-cluster-management"})
	sub3 := ClusterLifecycle(mch3, ovr)

	// 5. Activate HA mode
	mch4 := mch.DeepCopy()
	mch4.Spec.AvailabilityConfig = operatorsv1.HABasic
	sub4 := ClusterLifecycle(mch4, ovr)

	// 6. Modified CustomCAConfigmap
	mch6 := mch.DeepCopy()
	mch6.Spec.CustomCAConfigmap = ""
	sub5 := ClusterLifecycle(mch6, ovr)

	type args struct {
		found *unstructured.Unstructured
		want  *unstructured.Unstructured
	}
	tests := []struct {
		name  string
		args  args
		want  *unstructured.Unstructured
		want1 bool
	}{
		{
			name:  "Valid subscription",
			args:  args{sub, sub},
			want:  nil,
			want1: false,
		},
		{
			name:  "Modified ImagePullSecret",
			args:  args{sub, sub1},
			want:  sub1,
			want1: true,
		},
		{
			name:  "Modified ImagePullPolicy",
			args:  args{sub, sub2},
			want:  sub2,
			want1: true,
		},
		{
			name:  "Modified ImageRepository",
			args:  args{sub, sub3},
			want:  sub3,
			want1: true,
		},
		{
			name:  "Deactivate HighAvailabilityConfig mode",
			args:  args{sub, sub4},
			want:  sub4,
			want1: true,
		},
		{
			name:  "Modified CustomCAConfigmap",
			args:  args{sub, sub5},
			want:  sub5,
			want1: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := Validate(tt.args.found, tt.args.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Validate() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("Validate() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestSubscriptions(t *testing.T) {
	mch := &operatorsv1.MultiClusterHub{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test"},
		Spec: operatorsv1.MultiClusterHubSpec{
			ImagePullSecret: "test",
		},
	}
	ovr := map[string]string{}

	tests := []struct {
		name string
		got  *unstructured.Unstructured
	}{
		{"Console subscription", Console(mch, ovr, "")},
		{"Cluster Proxy Addon subscription", ClusterProxyAddon(mch, ovr, "")},
		{"GRC subscription", GRC(mch, ovr)},
		{"Insights subscription", Insights(mch, ovr, "")},
		{"ManagementIngress subscription", ManagementIngress(mch, ovr, "")},
		{"cluster-lifecycle subscription", ClusterLifecycle(mch, ovr)},
		{"Search subscription", Search(mch, ovr)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.got.MarshalJSON(); err != nil {
				t.Error("Subscription does not marshal properly")
			}
			_, err := yaml.Marshal(tt.got.Object["spec"])
			if err != nil {
				t.Error("Issue parsing subscription values")
			}
		})
	}
}

func TestOADPAnnotation(t *testing.T) {
	oadp := `{"channel": "stable-1.0", "installPlanApproval": "Automatic", "name": "redhat-oadp-operator", "source": "redhat-operators", "sourceNamespace": "openshift-marketplace", "startingCSV": "oadp-operator.v1.0.2"}`
	mch := &operatorsv1.MultiClusterHub{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Annotations: map[string]string{
				"installer.open-cluster-management.io/oadp-subscription-spec": oadp,
			},
		},
	}
	ovr := map[string]string{}
	appsub := ClusterBackup(mch, ovr)
	spec, err := yaml.Marshal(appsub.Object["spec"])
	if err != nil {
		t.Error("Issue parsing subscription values")
	}

	tests := []struct {
		name string
		want string
	}{
		{"Custom channel", "channel: stable-1.0"},
		{"Custom installPlan", "installPlanApproval: Automatic"},
		{"Custom name", "name: redhat-oadp-operator"},
		{"Custom source", "source: redhat-operators"},
		{"Custom sourceNamespace", "sourceNamespace: openshift-marketplace"},
		{"Custom startingCSV", "startingCSV: oadp-operator.v1.0.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(string(spec), tt.want) {
				t.Error(fmt.Sprintf("Cluster Backup missing OADP overrides. Got: %s, Want: %s", spec, tt.want))
			}
		})
	}

	mch = &operatorsv1.MultiClusterHub{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
		},
	}
	appsub = ClusterBackup(mch, ovr)
	spec, err = yaml.Marshal(appsub.Object["spec"])
	if err != nil {
		t.Error("Issue parsing subscription values")
	}
	if strings.Contains(string(spec), "oadpOperator") {
		t.Error(fmt.Sprintf("Cluster Backup should not have OADP overrides. Got: %s", spec))
	}
}
