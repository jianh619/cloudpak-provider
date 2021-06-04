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
	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"

	aimanagerv1alpha1 "github.ibm.com/katamari/katamari-installer/pkg/apis/orchestrator/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	knativ1alpha1 "knative.dev/operator/pkg/apis/operator/v1alpha1"
	knativeclient "knative.dev/operator/pkg/client/clientset/versioned/typed/operator/v1alpha1"

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
	DStrimzOperator         = "StrimzOperator"
	DStrimzOperatorName     = "strimzi-kafka-operator"
	DOpenshiftOperatorNS    = "openshift-operators"
	DServerlessOperator     = "ServerlessOperator"
	DServerlessOperatorName = "serverless-operator"
	DServerlessNamespace    = "ServerlessNamespace"
	DKnativeServingInstance = "KnativeServingInstance"
	DKnativeEveningInstance = "KnativeEveningInstance"

	CatalogSource    = "CatalogSource"
	OCPMarketplaceNS = "openshift-marketplace"

	AIOpsSubscription     = "AIOpsSubscription"
	OCPOperatorNS         = "openshift-operators"
	AIOpsSubscriptionName = "ibm-aiops-orchestrator"

	KNATIVE_SERVING_NAMESPACE  = "knative-serving"
	KNATIVE_EVENTING_NAMESPACE = "knative-eventing"

	KNATIVE_SERVING_INSTANCE_NAME  = "knative-serving"
	KNATIVE_EVENTING_INSTANCE_NAME = "knative-eventing"

	AIOpsInstallationName = "ibm-aiops"
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
	dependency = DNamespace
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

	//Check aiops namespace
	err := e.observeNamespace(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check image content policy
	/*
		err = e.observeImageContentPolicy(ctx)
		if err != nil {
			return managed.ExternalObservation{
				ResourceExists: false,
			}, nil
		}
	*/

	//Check image pull secret
	err = e.observeImagePullSecret(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check Strimz operator
	err = e.observeStrimzOperator(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check Serverless operator
	err = e.observeServerlessOperator(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check Knative installing namespace
	err = e.observeServerlessNamespace(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check Knative Serving instance
	err = e.observeKnativeServingInstance(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check Knative Eventing instance
	err = e.observeKnativeEventingInstance(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check Catalog resources
	err = e.observeCatalogSources(ctx)
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	//Check AIOPS subscription
	err = e.observeAIOpsSubscription(ctx)
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

	//Only handle unavailable resources/services/deployment
	switch {
	case dependency == DNamespace:
		e.createNamespace(ctx)
	case dependency == DImageContentPolicy:
		e.createImageContentPolicy(ctx)
	case dependency == DImagePullSecret:
		e.createImagePullSecret(ctx)
	case dependency == DStrimzOperator:
		e.createStrimzOperator(ctx)
	case dependency == DServerlessOperator:
		e.createServerlessOperator(ctx)
	case dependency == DServerlessNamespace:
		e.createServerlessNamespace(ctx)
	case dependency == DKnativeServingInstance:
		e.createKnativeServingInstance(ctx)
	case dependency == DKnativeEveningInstance:
		e.createKnativeEventingInstance(ctx)
	case dependency == CatalogSource:
		e.createCatalogSources(ctx)
	case dependency == AIOpsSubscription:
		e.createAIOpsSubscription(ctx)
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
	//dependency = DImageContentPolicy
	dependency = DImagePullSecret
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
	dependency = DStrimzOperator
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
		Type: "kubernetes.io/dockerconfigjson",
	}
	secretobj, err := e.kube.CoreV1().Secrets(aiopsNamespace).Create(context.TODO(), &secretSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		e.logger.Info("create imagePullSecret error , namespace : " + aiopsNamespace)
		return err
	}
	e.logger.Info("imagePullSecret created " + secretobj.Name)
	return nil
}

func (e *external) observeStrimzOperator(ctx context.Context) error {

	e.logger.Info("Observe StrimzOperator existing for aiops ")

	opaiops, err := e.opClient.OperatorsV1alpha1().
		Subscriptions(DOpenshiftOperatorNS).
		Get(ctx, DStrimzOperatorName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !(opaiops.Status.State == "AtLatestKnown") {
		//Return reconcile waiting for StrimzOperator ready
		e.logger.Info("Waiting for strimzi-kafka-operator operator AtLatestKnown")
		dependency = DStrimzOperator
		return nil
	}
	dependency = DServerlessOperator
	return nil
}

func (e *external) createStrimzOperator(ctx context.Context) error {
	e.logger.Info("Creating StrimzOperator for aiops ")
	subscription := &operatorv1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: DOpenshiftOperatorNS,
			Name:      DStrimzOperatorName,
		},
		Spec: &operatorv1alpha1.SubscriptionSpec{
			Channel:                "strimzi-0.19.x",
			InstallPlanApproval:    operatorv1alpha1.ApprovalAutomatic,
			CatalogSource:          "community-operators",
			CatalogSourceNamespace: "openshift-marketplace",
			Package:                "strimzi-kafka-operator",
		},
	}
	opStrimzi, err := e.opClient.OperatorsV1alpha1().
		Subscriptions("openshift-operators").
		Create(ctx, subscription, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	e.logger.Info("StrimzOperator subscription created " + opStrimzi.Name)
	return nil
}

func (e *external) observeServerlessOperator(ctx context.Context) error {

	e.logger.Info("Observe ServerlessOperator existing for aiops ")

	opaiops, err := e.opClient.OperatorsV1alpha1().
		Subscriptions(DOpenshiftOperatorNS).
		Get(ctx, DServerlessOperatorName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !(opaiops.Status.State == "AtLatestKnown") {
		//Return reconcile waiting for StrimzOperator ready
		e.logger.Info("Waiting for Serverless Operator AtLatestKnown")
		dependency = DServerlessOperator
		return nil
	}
	dependency = DServerlessNamespace
	return nil
}

func (e *external) createServerlessOperator(ctx context.Context) error {
	e.logger.Info("Creating ServerlessOperator for aiops ")

	subscription := &operatorv1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: DOpenshiftOperatorNS,
			Name:      DServerlessOperatorName,
		},
		Spec: &operatorv1alpha1.SubscriptionSpec{
			Channel:                "4.6",
			InstallPlanApproval:    operatorv1alpha1.ApprovalAutomatic,
			CatalogSource:          "redhat-operators",
			CatalogSourceNamespace: "openshift-marketplace",
			Package:                "serverless-operator",
		},
	}
	opServerless, err := e.opClient.OperatorsV1alpha1().
		Subscriptions(DOpenshiftOperatorNS).
		Create(ctx, subscription, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	e.logger.Info("StrimzOperator subscription created " + opServerless.Name)
	return nil
}

func (e *external) observeServerlessNamespace(ctx context.Context) error {

	e.logger.Info("Observe ServerlessNamespace existing for aiops " + KNATIVE_SERVING_NAMESPACE)

	_, err := e.kube.CoreV1().Namespaces().Get(context.TODO(), KNATIVE_SERVING_NAMESPACE, metav1.GetOptions{})
	if err != nil {
		return err
	}

	e.logger.Info("Observe ServerlessNamespace existing for aiops " + KNATIVE_EVENTING_NAMESPACE)
	_, err = e.kube.CoreV1().Namespaces().Get(context.TODO(), KNATIVE_EVENTING_NAMESPACE, metav1.GetOptions{})
	if err != nil {
		return err
	}

	dependency = DKnativeServingInstance
	return nil
}

func (e *external) createServerlessNamespace(ctx context.Context) error {
	e.logger.Info("Creating Namespace for aiops " + KNATIVE_SERVING_NAMESPACE)
	namespaceSource := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: KNATIVE_SERVING_NAMESPACE,
		},
	}
	namespaceobj, err := e.kube.CoreV1().Namespaces().Create(context.TODO(), namespaceSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		e.logger.Info("create namespace error , namespace : " + namespaceobj.Name)
		return err
	}

	e.logger.Info("Creating Namespace for aiops " + KNATIVE_EVENTING_NAMESPACE)
	namespaceSource = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: KNATIVE_EVENTING_NAMESPACE,
		},
	}
	namespaceobj, err = e.kube.CoreV1().Namespaces().Create(context.TODO(), namespaceSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		e.logger.Info("create namespace error , namespace : " + namespaceobj.Name)
		return err
	}

	e.logger.Info("Knative namespaces created ")
	return nil
}

func (e *external) observeKnativeServingInstance(ctx context.Context) error {

	e.logger.Info("Observe KnativeServingInstance existing for aiops ")

	knativeClient, err := knativeclient.NewForConfig(e.config)
	if err != nil {
		return nil
	}
	knservingInstance, err := knativeClient.KnativeServings(KNATIVE_SERVING_NAMESPACE).Get(ctx, KNATIVE_SERVING_INSTANCE_NAME, metav1.GetOptions{})
	if err != nil {
		e.logger.Info("Not able to list KnativeServingInstance , Namespace: " + KNATIVE_SERVING_NAMESPACE + ", KnativeServerInstance :" + KNATIVE_SERVING_INSTANCE_NAME)
		return err
	}
	if !(knservingInstance.Status.IsReady() == true) {
		//Return reconcile waiting for  knservingInstance ready
		e.logger.Info("KnativeServingInstance is not ready yet , Namespace: " + KNATIVE_SERVING_NAMESPACE + ", KnativeServerInstance :" + KNATIVE_SERVING_INSTANCE_NAME)
		return nil
	}
	dependency = DKnativeEveningInstance
	return nil
}

func (e *external) createKnativeServingInstance(ctx context.Context) error {
	e.logger.Info("Creating KnativeServingInstanc ")

	knativeClient, err := knativeclient.NewForConfig(e.config)
	if err != nil {
		return nil
	}

	selector := `
        selector:
          sdlc.visibility: cluster-local
`
	knserving := &knativ1alpha1.KnativeServing{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: KNATIVE_SERVING_NAMESPACE,
			Name:      KNATIVE_SERVING_INSTANCE_NAME,
			Labels:    map[string]string{"ibm-aiops-install/install": "knative-serving"},
		},
		Spec: knativ1alpha1.KnativeServingSpec{
			CommonSpec: knativ1alpha1.CommonSpec{
				Config: knativ1alpha1.ConfigMapData{
					"autoscaler": map[string]string{
						"enable-scale-to-zero": "true",
					},
					"domain": map[string]string{
						"svc.cluster.local": selector,
					},
				},
			},
		},
	}

	knservingInstance, err := knativeClient.KnativeServings(KNATIVE_SERVING_NAMESPACE).Create(ctx, knserving, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {

		return err
	}

	e.logger.Info("Knative namespaces created :" + knservingInstance.Name)
	return nil
}

func (e *external) observeKnativeEventingInstance(ctx context.Context) error {

	e.logger.Info("Observe KnativeEventingInstance existing for aiops ")

	knativeClient, err := knativeclient.NewForConfig(e.config)
	if err != nil {
		return nil
	}
	kneventingInstance, err := knativeClient.KnativeEventings(KNATIVE_EVENTING_NAMESPACE).Get(ctx, KNATIVE_EVENTING_INSTANCE_NAME, metav1.GetOptions{})
	if err != nil {
		e.logger.Info("Not able to list KnativeEventingInstance , Namespace: " + KNATIVE_EVENTING_NAMESPACE + ", KnativeEventingInstance :" + KNATIVE_EVENTING_INSTANCE_NAME)
		return err
	}
	if !(kneventingInstance.Status.IsReady() == true) {
		//Return reconcile waiting for  knservingInstance ready
		e.logger.Info("KnativeEventingInstance is not ready yet , Namespace: " + KNATIVE_EVENTING_NAMESPACE + ", KnativeEventingInstance :" + KNATIVE_EVENTING_INSTANCE_NAME)
		return nil
	}
	dependency = CatalogSource
	return nil
}

func (e *external) createKnativeEventingInstance(ctx context.Context) error {
	e.logger.Info("Creating KnativeEventingInstance ")

	knativeClient, err := knativeclient.NewForConfig(e.config)
	if err != nil {
		return nil
	}
	kneventing := &knativ1alpha1.KnativeEventing{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: KNATIVE_EVENTING_NAMESPACE,
			Name:      KNATIVE_EVENTING_INSTANCE_NAME,
			Labels:    map[string]string{"ibm-aiops-install/install": "knative-eventing"},
		},
	}

	kneventingInstance, err := knativeClient.KnativeEventings(KNATIVE_EVENTING_NAMESPACE).
		Create(ctx, kneventing, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	e.logger.Info("Knative namespaces created :" + kneventingInstance.Name)
	return nil
}

func (e *external) observeCatalogSources(ctx context.Context) error {

	e.logger.Info("Observe all CatalogSource existing for aiops ")

	//Check CatalogSources opencloud-operators
	opcs, err := e.opClient.OperatorsV1alpha1().
		CatalogSources(OCPMarketplaceNS).
		Get(ctx, "opencloud-operators", metav1.GetOptions{})
	if err != nil {
		e.logger.Info("Not able to get CatalogSources , Namespace: " + OCPMarketplaceNS + ", name : opencloud-operators ")
		return err
	}
	if !(opcs.Status.GRPCConnectionState.LastObservedState == "READY") {
		//Return reconcile waiting for  CatalogSources opencloud-operators ready
		e.logger.Info("CatalogSources opencloud-operators is not ready yet , Namespace: " + OCPMarketplaceNS + ", name : opencloud-operators ")
		return nil
	}

	//Check CatalogSources ibm-operator-catalog
	opcs, err = e.opClient.OperatorsV1alpha1().
		CatalogSources(OCPMarketplaceNS).
		Get(ctx, "ibm-operator-catalog", metav1.GetOptions{})
	if err != nil {
		e.logger.Info("Not able to get CatalogSources , Namespace: " + OCPMarketplaceNS + ", name : ibm-operator-catalog ")
		return err
	}
	if !(opcs.Status.GRPCConnectionState.LastObservedState == "READY") {
		//Return reconcile waiting for  CatalogSources ibm-operator-catalog ready
		e.logger.Info("CatalogSources ibm-operator-catalog is not ready yet , Namespace: " + OCPMarketplaceNS + ", name : ibm-operator-catalog ")
		return nil
	}

	//Check CatalogSources ibm-aiops-catalog
	opcs, err = e.opClient.OperatorsV1alpha1().
		CatalogSources(OCPMarketplaceNS).
		Get(ctx, "ibm-aiops-catalog", metav1.GetOptions{})
	if err != nil {
		e.logger.Info("Not able to get CatalogSources , Namespace: " + OCPMarketplaceNS + ", name : ibm-aiops-catalog ")
		return err
	}
	if !(opcs.Status.GRPCConnectionState.LastObservedState == "READY") {
		//Return reconcile waiting for  CatalogSources ibm-aiops-catalog ready
		e.logger.Info("CatalogSources ibm-aiops-catalog is not ready yet , Namespace: " + OCPMarketplaceNS + ", name : ibm-aiops-catalog ")
		return nil
	}

	dependency = AIOpsSubscription
	return nil
}

func (e *external) createCatalogSources(ctx context.Context) error {
	e.logger.Info("Creating all CatalogSources ")

	catalogSource := &operatorv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: OCPMarketplaceNS,
			Name:      "opencloud-operators",
		},
		Spec: operatorv1alpha1.CatalogSourceSpec{
			DisplayName: "IBMCS Operators",
			Image:       "docker.io/ibmcom/ibm-common-service-catalog:latest",
			Publisher:   "IBM",
			SourceType:  "grpc",
		},
	}

	_, err := e.opClient.OperatorsV1alpha1().
		CatalogSources(OCPMarketplaceNS).
		Create(ctx, catalogSource, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	catalogSource = &operatorv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: OCPMarketplaceNS,
			Name:      "ibm-operator-catalog",
		},
		Spec: operatorv1alpha1.CatalogSourceSpec{
			DisplayName: "ibm-operator-catalog",
			Image:       "docker.io/ibmcom/ibm-operator-catalog:latest",
			Publisher:   "IBM",
			SourceType:  "grpc",
		},
	}

	_, err = e.opClient.OperatorsV1alpha1().
		CatalogSources(OCPMarketplaceNS).
		Create(ctx, catalogSource, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	catalogSource = &operatorv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: OCPMarketplaceNS,
			Name:      "ibm-aiops-catalog",
		},
		Spec: operatorv1alpha1.CatalogSourceSpec{
			DisplayName: "IBM AIOps Catalog",
			Image:       "icr.io/cpopen/aiops-orchestrator-catalog:3.1-latest",
			Publisher:   "IBM",
			SourceType:  "grpc",
		},
	}

	_, err = e.opClient.OperatorsV1alpha1().
		CatalogSources(OCPMarketplaceNS).
		Create(ctx, catalogSource, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	e.logger.Info("All catalog resources are created ")
	return nil
}

func (e *external) observeAIOpsSubscription(ctx context.Context) error {

	e.logger.Info("Observe AIOPS Subscription existing for aiops ")

	//Check CatalogSources opencloud-operators
	opaiops, err := e.opClient.OperatorsV1alpha1().
		Subscriptions(OCPOperatorNS).
		Get(ctx, AIOpsSubscriptionName, metav1.GetOptions{})
	if err != nil {
		e.logger.Info("Not able to get  Subscriptions, Namespace: " + OCPOperatorNS + ", name : " + AIOpsSubscriptionName)
		return err
	}
	if !(opaiops.Status.State == "AtLatestKnown") {
		//Return reconcile waiting for  CatalogSources opencloud-operators ready
		e.logger.Info("Subscriptions is not ready yet , Namespace: " + OCPOperatorNS + ", name : " + AIOpsSubscriptionName)
		return nil
	}

	dependency = DNamespace
	return nil
}

func (e *external) createAIOpsSubscription(ctx context.Context) error {
	e.logger.Info("Creating all AIOPS subscription ")

	subscription := &operatorv1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: OCPOperatorNS,
			Name:      AIOpsSubscriptionName,
		},
		Spec: &operatorv1alpha1.SubscriptionSpec{
			Channel:                "v3.1",
			InstallPlanApproval:    operatorv1alpha1.ApprovalAutomatic,
			CatalogSource:          "ibm-aiops-catalog",
			CatalogSourceNamespace: OCPMarketplaceNS,
			Package:                "ibm-aiops-orchestrator",
		},
	}
	subscription, err := e.opClient.OperatorsV1alpha1().
		Subscriptions(OCPOperatorNS).
		Create(ctx, subscription, metav1.CreateOptions{})

	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	e.logger.Info("AIOPS subscription are created ")
	return nil
}

func (e *external) observeAIOpsInstance(ctx context.Context) error {

	e.logger.Info("Observe AIOPS Instance existing for aiops ")

	options := ctrl.Options{Scheme: cloudpakscheme}
	cloudpackClient, err := client.New(config, client.Options{Scheme: options.Scheme})
	if err != nil {
		e.logger.Info("create cloudpack client error ")
	}
	aiopsinstallation := &aimanagerv1alpha1.Installation{}
	err = cloudpackClient.Get(ctx, types.NamespacedName{
		Namespace: aiopsNamespace,
		Name:      AIOpsInstallationName,
	}, aiopsinstallation)

	if err != nil {
		e.logger.Info("Get aiopsinstallation faild")
		return err
	}
	if !(aiopsinstallation.Status.Phase == "Running") {
		//Return reconcile waiting for  AI manager ready
		return nil
	}

	//Check pod count of Running status
	runningPod, err := e.kube.CoreV1().Pods(aiopsNamespace).List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase==Running",
	})
	if err != nil {
		e.logger.Info("Get aiopsinstallation pods faild in  namespace : " + aiopsNamespace)
		return nil
	}

	e.logger.Info("current running pods num is " + len(runningPod.Items))

	dependency = DNamespace
	return nil
}

func (e *external) createAIOpsInstance(ctx context.Context) error {
	e.logger.Info("Creating all AIOPS instance ")

	options := ctrl.Options{Scheme: cloudpakscheme}
	cloudpackClient, err := client.New(config, client.Options{Scheme: options.Scheme})
	if err != nil {
		e.logger.Info("create cloudpack client error ")
		return err
	}

	const isEnabled = false
	enabled := isEnabled
	storageClass := StorageClassName
	nonSharedStorageClass := StorageClassName

	aimanager := &aimanagerv1alpha1.Installation{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: aiopsNamespace,
			Name:      AIOpsInstallationName,
		},
		Spec: aimanagerv1alpha1.InstallationSpec{
			Size: "small",
			License: aimanagerv1alpha1.License{
				Accept: true,
			},
			StorageClass:           storageClass,
			StorageClassLargeBlock: nonSharedStorageClass,
			Modules: []aimanagerv1alpha1.PakModule{
				{
					Name:    "aiManager",
					Enabled: &enabled,
				},
				{
					Name:    "aiopsFoundation",
					Enabled: &enabled,
				},
				{
					Name:    "applicationManager",
					Enabled: &enabled,
				},
			},
		},
	}

	err = cloudpackClient.Create(ctx, aimanager)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		e.logger.Info("Create aimanager instance error ")
		return err
	}

	e.logger.Info("AIOPS instance are created ")
	return nil
}
