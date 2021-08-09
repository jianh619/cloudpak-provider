module github.com/jianh619/cloudpak-provider

go 1.16

replace (
        github.com/operator-framework/operator-sdk => github.com/operator-framework/operator-sdk v0.17.0
        k8s.io/api => k8s.io/api v0.19.3
        k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.3
        k8s.io/apimachinery => k8s.io/apimachinery v0.19.3
        k8s.io/apiserver => k8s.io/apiserver v0.19.3
        k8s.io/cli-runtime => k8s.io/cli-runtime v0.19.3
        k8s.io/client-go => k8s.io/client-go v0.19.3 // Required by prometheus-operator
        k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.3
        k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.19.3
        k8s.io/code-generator => k8s.io/code-generator v0.19.3
        k8s.io/component-base => k8s.io/component-base v0.19.3
        k8s.io/cri-api => k8s.io/cri-api v0.19.3
        k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.19.3
        k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.19.3
        k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.19.3
        k8s.io/kube-proxy => k8s.io/kube-proxy v0.19.3
        k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.19.3
        k8s.io/kubectl => k8s.io/kubectl v0.19.3
        k8s.io/kubelet => k8s.io/kubelet v0.19.3
        k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.19.3
        k8s.io/metrics => k8s.io/metrics v0.19.3
        k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.19.3
)

require (
        github.com/crossplane/crossplane-runtime v0.13.0
        github.com/crossplane/crossplane-tools v0.0.0-20210320162312-1baca298c527
        github.com/openshift/api v3.9.1-0.20190924102528-32369d4db2ad+incompatible
        github.com/openshift/client-go v0.0.0-20200326155132-2a6cd50aedd0
        github.com/operator-framework/api v0.8.0
        github.com/operator-framework/operator-lifecycle-manager v0.18.1
        github.com/pkg/errors v0.9.1
        github.ibm.com/katamari/katamari-installer v0.0.0-20210722124411-966a1643c68b
        gopkg.in/alecthomas/kingpin.v2 v2.2.6
        k8s.io/api v0.21.2
        k8s.io/apiextensions-apiserver v0.21.2 // indirect
        k8s.io/apimachinery v0.21.2
        k8s.io/client-go v12.0.0+incompatible
        knative.dev/operator v0.24.0
        sigs.k8s.io/controller-runtime v0.8.3
        sigs.k8s.io/controller-tools v0.6.1
)

