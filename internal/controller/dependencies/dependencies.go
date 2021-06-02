/*
Copyright 2020 The Crossplane Authors.

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

package dependencies

import (
	"context"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/jianh619/cloudpak-provider/apis/dependency/v1alpha1"
	apisv1alpha1 "github.com/jianh619/cloudpak-provider/apis/v1alpha1"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	openshiftv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	ocpoperatorv1alpha1Client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	operatorclient "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
)

const (
	errNotDependency     = "managed resource is not a Dependency custom resource"
	errTrackPCUsage      = "cannot track ProviderConfig usage"
	errGetPC             = "cannot get ProviderConfig"
	errGetCreds          = "cannot get credentials"
	errObserveDependency = "observe dependencies error"
	errCreateDependency  = "create dependencies error"

	errNewClient   = "cannot create new Service"
	aiopsNamespace = "aiops"

	DNamespace              = "Namespace"
	DImageContentPolicy     = "ImageContentPolicy"
	DImageContentPolicyName = "mirror-config"
	DImagePullSecret        = "ImagePullSecret"
	DImagePullSecretName    = "ibm-entitlement-key"
)

// A NoOpService does nothing.
type NoOpService struct{}

var (
	newNoOpService = func(_ []byte) (interface{}, error) { return &NoOpService{}, nil }

	registries = map[string][]string{
		"cp.icr.io/cp":        {"cp.stg.icr.io/cp"},
		"docker.io/ibmcom":    {"cp.stg.icr.io/cp"},
		"quay.io/opencloudio": {"hyc-cloud-private-daily-docker-local.artifactory.swg-devops.com/ibmcom"},
		"cp.icr.io/cp/cpd":    {"hyc-cloud-private-daily-docker-local.artifactory.swg-devops.com/ibmcom"},
	}
	dependency = ""
)

// Setup adds a controller that reconciles Dependency managed resources.
func Setup(mgr ctrl.Manager, l logging.Logger, rl workqueue.RateLimiter) error {
	name := managed.ControllerName(v1alpha1.DependencyGroupKind)
	logger := l.WithValues("controller", name)

	o := controller.Options{
		RateLimiter: ratelimiter.NewDefaultManagedRateLimiter(rl),
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.DependencyGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			logger: logger,
			kube:   mgr.GetClient(),
			usage:  resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
		}),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o).
		For(&v1alpha1.Dependency{}).
		Complete(r)
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	logger logging.Logger
	kube   client.Client
	usage  resource.Tracker
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Dependency)
	if !ok {
		return nil, errors.New(errNotDependency)
	}

	logger := c.logger.WithValues("request", cr.Name)

	logger.Info("Connecting")

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	cd := pc.Spec.Credentials
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	clientConfig, err := clientcmd.RESTConfigFromKubeConfig(data)

	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		panic(err)
	}

	opClientInstance, err := operatorclient.NewForConfig(clientConfig)
	if err != nil {
		panic(err)
	}

	//Get the imagepullsecret data in same namespace
	cd.CommonCredentialSelectors.SecretRef.Name = DImagePullSecretName
	data, err = resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)

	return &external{
		logger:     logger,
		localKube:  c.kube,
		kube:       kubeClient,
		config:     clientConfig,
		opClient:   opClientInstance,
		pullsecret: data,
	}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	logger     logging.Logger
	localKube  client.Client
	kube       *kubernetes.Clientset
	config     *rest.Config
	opClient   *operatorclient.Clientset
	pullsecret []byte
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Dependency)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotDependency)
	}

	// These fmt statements should be removed in the real implementation.
	e.logger.Info("Observing: " + cr.ObjectMeta.Name)

	//this is for test
	//err := e.observeTest()

	err := errors.New("This is just for init ")

	if dependency == "" {
		dependency = DNamespace
	}

	switch {
	case dependency == DNamespace:
		err = e.observeNamespace(ctx)
	case dependency == DImageContentPolicy:
		err = e.observeImageContentPolicy(ctx)
	case dependency == DImagePullSecret:
		err = e.observeImagePullSecret(ctx)
	}

	//this is for real deployment
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}
	cr.Status.SetConditions(xpv1.Available())
	return managed.ExternalObservation{
		// Return false when the external resource does not exist. This lets
		// the managed resource reconciler know that it needs to call Create to
		// (re)create the resource, or that it has successfully been deleted.
		ResourceExists: true,

		// Return false when the external resource exists, but it not up to date
		// with the desired managed resource state. This lets the managed
		// resource reconciler know that it needs to call Update.
		ResourceUpToDate: true,

		// Return any details that may be required to connect to the external
		// resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Dependency)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotDependency)
	}

	e.logger.Info("Creating: " + cr.ObjectMeta.Name)

	//this is for test
	//e.createTest()

	//this is for real deployment
	switch {
	case dependency == DNamespace:
		e.createNamespace(ctx)
	case dependency == DImageContentPolicy:
		e.createImageContentPolicy(ctx)
	case dependency == DImagePullSecret:
		e.createImagePullSecret(ctx)
	}

	return managed.ExternalCreation{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Dependency)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotDependency)
	}

	e.logger.Info("Updating: " + cr.ObjectMeta.Name)

	return managed.ExternalUpdate{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Dependency)
	if !ok {
		return errors.New(errNotDependency)
	}

	e.logger.Info("Deleting: " + cr.ObjectMeta.Name)

	return nil
}

func int32Ptr(i int32) *int32 { return &i }

func (e *external) observeNamespace(ctx context.Context) error {

	e.logger.Info("Observe Namespace existing for aiops " + aiopsNamespace)

	_, err := e.kube.CoreV1().Namespaces().Get(context.TODO(), aiopsNamespace, metav1.GetOptions{})
	if err != nil {
		return err
	}
	dependency = DImageContentPolicy
	return nil
}

func (e *external) createNamespace(ctx context.Context) error {
	e.logger.Info("Creating Namespace for aiops " + aiopsNamespace)
	namespaceSource := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: aiopsNamespace,
		},
	}
	namespaceobj, err := e.kube.CoreV1().Namespaces().Create(context.TODO(), namespaceSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		e.logger.Info("create namespace error , namespace : " + aiopsNamespace)
		return err
	}
	e.logger.Info("namespace created " + namespaceobj.Name)
	return nil
}

func (e *external) observeImageContentPolicy(ctx context.Context) error {

	e.logger.Info("Observe ImageContentPolicy existing for aiops ")

	openshiftClient, err := ocpoperatorv1alpha1Client.NewForConfig(e.config)
	if err != nil {
		e.logger.Info("Create ocp client error")
		return err
	}
	_, err = openshiftClient.ImageContentSourcePolicies().Get(ctx, DImageContentPolicyName, metav1.GetOptions{})
	if err != nil {
		e.logger.Info("Get ImageContentPolicy error")
		return err
	}
	dependency = DImagePullSecret
	return nil
}

func (e *external) createImageContentPolicy(ctx context.Context) error {

	e.logger.Info("Create ImageContentPolicy for aiops ")

	openshiftClient, err := ocpoperatorv1alpha1Client.NewForConfig(e.config)
	if err != nil {
		e.logger.Info("Create ocp client error")
		return err
	}

	mirrors := []openshiftv1alpha1.RepositoryDigestMirrors{}
	for source, mirror := range registries {
		mirrors = append(mirrors, openshiftv1alpha1.RepositoryDigestMirrors{
			Source:  source,
			Mirrors: mirror,
		})
	}

	imagepolicy := &openshiftv1alpha1.ImageContentSourcePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: DImageContentPolicyName,
		},
		Spec: openshiftv1alpha1.ImageContentSourcePolicySpec{
			RepositoryDigestMirrors: mirrors,
		},
	}

	imageContentSourcePolicy, err := openshiftClient.ImageContentSourcePolicies().Create(ctx, imagepolicy, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		e.logger.Info("Create imageContentSourcePolicy error")
		return err
	}
	e.logger.Info("ImageContentSourcePolicy created : " + imageContentSourcePolicy.Name)
	return nil
}

func (e *external) observeImagePullSecret(ctx context.Context) error {

	e.logger.Info("Observe ImagePullSecret existing for aiops ")

	_, err := e.kube.CoreV1().Secrets(aiopsNamespace).Get(ctx, DImagePullSecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	dependency = DNamespace
	return nil
}

func (e *external) createImagePullSecret(ctx context.Context) error {
	e.logger.Info("Creating ImagePullSecret for aiops ")
	secretSource := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DImagePullSecretName,
			Namespace: aiopsNamespace,
		},
		Data: map[string][]byte{
			".dockerconfigjson": e.pullsecret,
		},
	}
	secretobj, err := e.kube.CoreV1().Secrets(aiopsNamespace).Create(context.TODO(), &secretSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		e.logger.Info("create namespace error , namespace : " + aiopsNamespace)
		return err
	}
	e.logger.Info("imagePullSecret created " + secretobj.Name)
	return nil
}

