module github.com/giantswarm/cluster-cleaner

go 1.16

require (
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.16.5 // indirect
	github.com/onsi/gomega v1.17.0 // indirect
	github.com/pkg/errors v0.9.1
	github.com/stretchr/testify v1.7.0
	k8s.io/api v0.22.4
	k8s.io/apimachinery v0.22.4
	k8s.io/client-go v0.22.4
	sigs.k8s.io/cluster-api v0.3.22
	sigs.k8s.io/controller-runtime v0.10.3
)
