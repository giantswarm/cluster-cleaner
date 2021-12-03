package controllers

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/cluster-api/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	fakeScheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(fakeScheme))
	_ = v1alpha3.AddToScheme(fakeScheme)
}

func TestClusterController(t *testing.T) {
	testCases := []struct {
		name                   string
		expectedDeletion       bool
		expectedEventTriggered bool

		cluster *v1alpha3.Cluster
	}{
		// cluster marked for deletion
		{
			name:                   "case 0",
			expectedDeletion:       true,
			expectedEventTriggered: false,

			cluster: &v1alpha3.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						time.Now().Add(-defaultTTL),
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

			cluster: &v1alpha3.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						time.Now().Add(-9 * time.Hour),
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

			cluster: &v1alpha3.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						time.Now().Add(-eventDefaultTTL),
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

			cluster: &v1alpha3.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
					CreationTimestamp: metav1.Time{
						time.Now().Add(-eventDefaultTTL),
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
			}
			ctx := context.TODO()
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tc.cluster.GetName(), Namespace: tc.cluster.GetNamespace()}})
			if err != nil {
				t.Error(err)
			}

			obj := &v1alpha3.Cluster{}
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
