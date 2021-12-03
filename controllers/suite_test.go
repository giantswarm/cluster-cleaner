package controllers

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/cluster-api/api/v1alpha3"
)

var (
	fakeScheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(fakeScheme))
	_ = v1alpha3.AddToScheme(fakeScheme)
}

func TestClusterController(t *testing.T) {
	//TODO
}
