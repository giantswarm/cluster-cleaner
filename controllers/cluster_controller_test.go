package controllers

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	gsapplication "github.com/giantswarm/apiextensions-application/api/v1alpha1"
	"github.com/giantswarm/k8smetadata/pkg/label"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	fakeScheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(fakeScheme))
	_ = capi.AddToScheme(fakeScheme)
	_ = gsapplication.AddToScheme(fakeScheme)
}

func TestClusterController(t *testing.T) {
	testCases := []struct {
		name                   string
		dryRun                 bool
		expectedDeletion       bool
		expectedEventTriggered bool

		cluster *capi.Cluster
	}{
		// cluster marked for deletion
		{
			name:                   "case 0",
			expectedDeletion:       true,
			expectedEventTriggered: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-defaultTTL),
					},
					Annotations: map[string]string{},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
		},
		// cluster not yet marked for deletion
		{
			name:                   "case 1",
			expectedDeletion:       false,
			expectedEventTriggered: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-11 * time.Hour),
					},
					Annotations: map[string]string{},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
		},
		// only send event for a cluster being marked for deletion
		{
			name:                   "case 2",
			expectedDeletion:       false,
			expectedEventTriggered: true,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-eventDefaultTTL),
					},
					Annotations: map[string]string{},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
		},
		// cluster will be ignored
		{
			name:                   "case 3",
			expectedDeletion:       false,
			expectedEventTriggered: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-eventDefaultTTL),
					},
					Annotations: map[string]string{
						ignoreClusterDeletion: "true",
					},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
		},
		// only dry-run mode
		{
			name:                   "case 4",
			dryRun:                 true,
			expectedDeletion:       false,
			expectedEventTriggered: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-defaultTTL),
					},
					Annotations: map[string]string{},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
		},
		// keep-until label has not expired and but the defaultTTL for cluster deletion has expired
		// cluster should be kept
		{
			name:                   "case 5",
			dryRun:                 false,
			expectedDeletion:       false,
			expectedEventTriggered: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-24 * time.Hour),
					},
					Annotations: map[string]string{},
					Labels: map[string]string{
						keepUntil: "2099-12-01",
					},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
		},
		// keep-until label has expired and cluster will be deleted
		{
			name:                   "case 6",
			dryRun:                 false,
			expectedDeletion:       true,
			expectedEventTriggered: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-12 * time.Hour),
					},
					Annotations: map[string]string{},
					Labels: map[string]string{
						keepUntil: "2020-12-08",
					},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
		},
	}
	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(fakeScheme).WithObjects(tc.cluster).Build()
			fakeRecorder := record.NewFakeRecorder(1)
			r := &ClusterReconciler{
				Client:   fakeClient,
				Scheme:   fakeScheme,
				Log:      ctrl.Log.WithName("fake"),
				recorder: fakeRecorder,
				DryRun:   tc.dryRun,
			}
			ctx := context.TODO()
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tc.cluster.GetName(), Namespace: tc.cluster.GetNamespace()}})
			if err != nil {
				t.Error(err)
			}

			obj := &capi.Cluster{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: tc.cluster.GetName(), Namespace: tc.cluster.GetNamespace()}, obj)
			if err != nil {
				t.Error(err)
			}

			if tc.expectedDeletion && obj.DeletionTimestamp == nil {
				t.Errorf("expected deletion timestamp to be set")
			}

			triggered := false
			for eventsLeft := true; eventsLeft; {
				select {
				case event := <-fakeRecorder.Events:
					if strings.Contains(event, "ClusterMarkedForDeletion") {
						t.Log(event)
						triggered = true
					} else {
						t.Fatalf("test case %v failed. unexpected event %v", tc.name, event)
					}
				default:
					eventsLeft = false
				}
			}
			assert.Equal(t, tc.expectedEventTriggered, triggered, "test case %v failed.", tc.name)
		})
	}
}

func TestClusterAppDeletion(t *testing.T) {
	testCases := []struct {
		name                    string
		dryRun                  bool
		expectedClusterDeletion bool

		cluster *capi.Cluster
		apps    []struct {
			app              *gsapplication.App
			expectedDeletion bool
		}
	}{
		// app marked for deletion
		{
			name:                    "case 0 - app delete",
			expectedClusterDeletion: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-defaultTTL),
					},
					Annotations: map[string]string{
						helmReleaseNameAnnotation:      "test",
						helmReleaseNamespaceAnnotation: "default",
					},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
			apps: []struct {
				app              *gsapplication.App
				expectedDeletion bool
			}{
				{
					expectedDeletion: true,
					app: &gsapplication.App{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: "default",
							Labels: map[string]string{
								label.Cluster: "test",
							},
							Finalizers: []string{
								"test.giantswarm.io/keep",
							},
						},
					},
				},
			},
		},
		// nothing marked for deletion
		{
			name:                    "case 1 - no delete",
			expectedClusterDeletion: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(defaultTTL),
					},
					Annotations: map[string]string{
						helmReleaseNameAnnotation:      "test",
						helmReleaseNamespaceAnnotation: "default",
					},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
			apps: []struct {
				app              *gsapplication.App
				expectedDeletion bool
			}{
				{
					expectedDeletion: false,
					app: &gsapplication.App{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: "default",
							Labels: map[string]string{
								label.Cluster: "test",
							},
							Finalizers: []string{
								"test.giantswarm.io/keep",
							},
						},
					},
				},
			},
		},
		// cluster marked for deletion
		{
			name:                    "case 2 - cluster delete",
			expectedClusterDeletion: true,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-defaultTTL),
					},
					Annotations: map[string]string{
						helmReleaseNameAnnotation:      "test",
						helmReleaseNamespaceAnnotation: "default",
					},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
			apps: []struct {
				app              *gsapplication.App
				expectedDeletion bool
			}{},
		},
		// multiple apps marked for deletion
		{
			name:                    "case 3 - multiple app delete",
			expectedClusterDeletion: false,

			cluster: &capi.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-defaultTTL),
					},
					Annotations: map[string]string{
						helmReleaseNameAnnotation:      "test",
						helmReleaseNamespaceAnnotation: "default",
					},
					Finalizers: []string{
						"operatorkit.giantswarm.io/cluster-operator-cluster-controller",
					},
				},
			},
			apps: []struct {
				app              *gsapplication.App
				expectedDeletion bool
			}{
				{
					expectedDeletion: true,
					app: &gsapplication.App{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: "default",
							Labels: map[string]string{
								label.Cluster: "test",
							},
							Finalizers: []string{
								"test.giantswarm.io/keep",
							},
						},
					},
				},
				{
					expectedDeletion: true,
					app: &gsapplication.App{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "default-apps",
							Namespace: "default",
							Labels: map[string]string{
								label.Cluster: "test",
							},
							Finalizers: []string{
								"test.giantswarm.io/keep",
							},
						},
					},
				},
				{
					expectedDeletion: false,
					app: &gsapplication.App{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "coredns",
							Namespace: "default",
							Labels: map[string]string{
								label.Cluster:                  "test",
								"app.kubernetes.io/managed-by": "Helm",
							},
							Finalizers: []string{
								"test.giantswarm.io/keep",
							},
						},
					},
				},
			},
		},
	}
	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			fakeClientBuilder := fake.NewClientBuilder().WithScheme(fakeScheme).WithObjects(tc.cluster)
			for _, app := range tc.apps {
				fakeClientBuilder = fakeClientBuilder.WithObjects(app.app)
			}
			fakeClient := fakeClientBuilder.Build()
			fakeRecorder := record.NewFakeRecorder(1)
			r := &ClusterReconciler{
				Client:   fakeClient,
				Scheme:   fakeScheme,
				Log:      ctrl.Log.WithName("fake"),
				recorder: fakeRecorder,
				DryRun:   tc.dryRun,
			}
			ctx := context.TODO()
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tc.cluster.GetName(), Namespace: tc.cluster.GetNamespace()}})
			if err != nil {
				t.Error(err)
			}

			cluster := &capi.Cluster{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: tc.cluster.GetName(), Namespace: tc.cluster.GetNamespace()}, cluster)
			if err != nil {
				t.Error(err)
			}

			if tc.expectedClusterDeletion && cluster.DeletionTimestamp == nil {
				t.Errorf("expected deletion timestamp to be set on cluster")
			}

			if !tc.expectedClusterDeletion {
				for _, a := range tc.apps {
					app := &gsapplication.App{}
					err = fakeClient.Get(ctx, types.NamespacedName{Name: a.app.GetName(), Namespace: a.app.GetNamespace()}, app)
					if err != nil {
						t.Error(err)
					}

					if a.expectedDeletion && app.DeletionTimestamp == nil {
						t.Errorf("expected deletion timestamp to be set on app")
					} else if !a.expectedDeletion && app.DeletionTimestamp != nil {
						t.Errorf("not expecting deletion timestamp to be set on app")
					}
				}
			}
			if t.Failed() {
				t.Logf("Test case '%s' failed", tc.name)
			}
		})
	}
}
