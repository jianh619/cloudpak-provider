# cloudpak-provider

`cloudpake-provider` is a [Crossplane](https://crossplane.io/) Provider 
, which helps preparing dependencies and installing cloudpak .


## Getting Started

### Prerequisites

- You need install a kubernetes cluster https://kubernetes.io/
- You need install crossplane https://crossplane.io in above kubernetes cluster
- You need a target openshift cluster , will install cloudpak

## Play

Following executing are all in cluster which installing crossplane 

### Create secret storing the kubeconfig 

Using the kubeconfig in this repo as example :

```
kubectl create secret generic example-provider-secret --from-file=credentials=./examples/provider/kubeconfig
```

You need replace the sample kubeconfig with your target kubeconfig 

### Create secret storing the imagepullsecret 

You can directly use the repo one as below :

```
kubectl apply -f examples/dependency/imagePullSecret.yaml
```

### Create provider config 

```
kubectl apply -f examples/provider/config.yaml
```

### Create a Dependency CR to prepare dependencies in target cluster

```
kubectl apply -f examples/dependency/dependency.yaml
```

### Start the provider to watch procedure

```
make run 
```

### Install provider-helm 

Since I use helm-chart to deploy aiops intallation , so you need install provider-helm 

```
cat << EOF | oc apply -f -
apiVersion: pkg.crossplane.io/v1alpha1
kind: Provider
metadata:
  name: provider-helm
spec:
  package: "crossplane/provider-helm:master"
EOF
```

And create a corresponding provider-config use the secret storing your target kubeconfig

```
cat << EOF | oc apply -f -
apiVersion: helm.crossplane.io/v1beta1
kind: ProviderConfig
metadata:
  name: helm-provider
spec:
  credentials:
    source: Secret
    secretRef:
      name: example-provider-secret
      namespace: crossplane-system
      key: credentials
EOF      
```

### Install aiops 

```
kubectl apply -f examples/aiops/installation.yaml 
```

Note: you can modify the values in yaml to customize your installation 

