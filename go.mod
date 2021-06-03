module github.com/jianh619/cloudpak-provider

go 1.13

require (
	github.com/crossplane/crossplane-runtime v0.13.0
	github.com/crossplane/crossplane-tools v0.0.0-20201201125637-9ddc70edfd0d
	github.com/openshift/api v0.0.0-20200331152225-585af27e34fd
	github.com/openshift/client-go v0.0.0-20200326155132-2a6cd50aedd0
	github.com/operator-framework/api v0.8.0
	github.com/operator-framework/operator-lifecycle-manager v0.18.1
	github.com/pkg/errors v0.9.1
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	k8s.io/api v0.20.6
	k8s.io/apimachinery v0.20.6
	k8s.io/client-go v0.20.6
	knative.dev/operator v0.23.0
	sigs.k8s.io/controller-runtime v0.8.3
	sigs.k8s.io/controller-tools v0.4.1
)
