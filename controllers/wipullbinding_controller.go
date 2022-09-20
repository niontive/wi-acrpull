/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	wiacrpullv1 "github.com/niontive/wi-acrpull/api/v1"
	"github.com/niontive/wi-acrpull/pkg/authorizer"
	"github.com/niontive/wi-acrpull/pkg/authorizer/types"
	"github.com/pkg/errors"
)

const (
	ownerKey                  = ".metadata.controller"
	wiAcrPullFinalizerName    = "wi-acrpull.microsoft.com"
	defaultServiceAccountName = "default"
	dockerConfigKey           = ".dockerconfigjson"

	tokenRefreshBuffer = time.Minute * 30
)

// WIpullbindingReconciler reconciles a WIpullbinding object
type WIpullbindingReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=wi-acrpull.microsoft.com,resources=wipullbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=wi-acrpull.microsoft.com,resources=wipullbindings/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=wi-acrpull.microsoft.com,resources=wipullbindings/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the WIpullbinding object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *WIpullbindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// TODO(user): your logic here
	var acrBinding wiacrpullv1.WIpullbinding
	if err := r.Get(ctx, req.NamespacedName, &acrBinding); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to fetch acrPullBinding.")
			return ctrl.Result{}, err
		}
		log.Info("AcrPullBinding is not found. Ignore because this is expected to happen when it is being deleted.")
		return ctrl.Result{}, nil
	}

	serviceAccountName := getServiceAccountName(acrBinding.Spec.ServiceAccountName)

	// examine DeletionTimestamp to determine if acr pull binding is under deletion
	if acrBinding.ObjectMeta.DeletionTimestamp.IsZero() {
		// the object is not being deleted, so if it does not have our finalizer,
		// then need to add the finalizer and update the object.
		if err := r.addFinalizer(ctx, &acrBinding, log); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		// the object is being deleted
		if err := r.removeFinalizer(ctx, &acrBinding, req, serviceAccountName, log); err != nil {
			return ctrl.Result{}, err
		}

		// stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	clientID := acrBinding.Spec.ServicePrincipalClientID
	tenantID := acrBinding.Spec.ServicePrincipalTenantID
	acrServer := acrBinding.Spec.AcrServer

	acrAccessToken, err := authorizer.AcquireACRAccessToken(ctx, clientID, tenantID, acrServer)
	if err != nil {
		log.Error(err, "Failed to get ACR access token")
		if err := r.setErrStatus(ctx, err, &acrBinding); err != nil {
			log.Error(err, "Failed to update error status")
		}

		return ctrl.Result{}, err
	}

	dockerConfig := authorizer.CreateACRDockerCfg(acrServer, acrAccessToken)

	var pullSecrets v1.SecretList
	if err := r.List(ctx, &pullSecrets, client.InNamespace(req.Namespace), client.MatchingFields{ownerKey: req.Name}); err != nil {
		log.Error(err, "unable to list child secrets")
		return ctrl.Result{}, err
	}
	pullSecret := getPullSecret(&acrBinding, pullSecrets.Items)

	// Create a new secret if one doesn't already exist
	if pullSecret == nil {
		log.Info("Creating new pull secret")

		pullSecret, err := newBasePullSecret(&acrBinding, dockerConfig, r.Scheme)
		if err != nil {
			log.Error(err, "Failed to construct pull secret")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, pullSecret); err != nil {
			log.Error(err, "Failed to create pull secret in cluster")
			return ctrl.Result{}, err
		}
	} else {
		log.Info("Updating existing pull secret")

		pullSecret := updatePullSecret(&pullSecrets.Items[0], dockerConfig)
		if err := r.Update(ctx, pullSecret); err != nil {
			log.Error(err, "Failed to update pull secret")
			return ctrl.Result{}, err
		}
	}

	// Associate the image pull secret with the default service account of the namespace
	if err := r.updateServiceAccount(ctx, &acrBinding, req, serviceAccountName, log); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.setSuccessStatus(ctx, &acrBinding, acrAccessToken); err != nil {
		log.Error(err, "Failed to update acr binding status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{
		RequeueAfter: getTokenRefreshDuration(acrAccessToken),
	}, nil
}

func (r *WIpullbindingReconciler) updateServiceAccount(ctx context.Context, acrBinding *wiacrpullv1.WIpullbinding,
	req ctrl.Request, serviceAccountName string, log logr.Logger) error {
	var serviceAccount v1.ServiceAccount
	saNamespacedName := k8stypes.NamespacedName{
		Namespace: req.Namespace,
		Name:      serviceAccountName,
	}
	if err := r.Get(ctx, saNamespacedName, &serviceAccount); err != nil {
		log.Error(err, "Failed to get service account")
		return err
	}
	pullSecretName := getPullSecretName(acrBinding.Name)
	if !imagePullSecretRefExist(serviceAccount.ImagePullSecrets, pullSecretName) {
		log.Info("Updating default service account")
		appendImagePullSecretRef(&serviceAccount, pullSecretName)
		if err := r.Update(ctx, &serviceAccount); err != nil {
			log.Error(err, "Failed to append image pull secret reference to default service account", "pullSecretName", pullSecretName)
			return err
		}
	}
	return nil
}

func (r *WIpullbindingReconciler) setErrStatus(ctx context.Context, err error, acrBinding *wiacrpullv1.WIpullbinding) error {
	acrBinding.Status.Error = err.Error()
	if err := r.Status().Update(ctx, acrBinding); err != nil {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WIpullbindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1.Secret{}, ownerKey, func(rawObj client.Object) []string {
		secret := rawObj.(*v1.Secret)
		owner := metav1.GetControllerOf(secret)
		if owner == nil {
			return nil
		}

		if owner.APIVersion != wiacrpullv1.GroupVersion.String() || owner.Kind != "WIpullbinding" {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&wiacrpullv1.WIpullbinding{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}). // Needed to not enter reconcile loop on status update
		Owns(&v1.Secret{}).
		Complete(r)
}

func (r *WIpullbindingReconciler) addFinalizer(ctx context.Context, acrBinding *wiacrpullv1.WIpullbinding, log logr.Logger) error {
	if !containsString(acrBinding.ObjectMeta.Finalizers, wiAcrPullFinalizerName) {
		acrBinding.ObjectMeta.Finalizers = append(acrBinding.ObjectMeta.Finalizers, wiAcrPullFinalizerName)
		if err := r.Update(ctx, acrBinding); err != nil {
			log.Error(err, "Failed to append acr pull binding finalizer", "finalizerName", wiAcrPullFinalizerName)
			return err
		}
	}
	return nil
}

func (r *WIpullbindingReconciler) removeFinalizer(ctx context.Context, acrBinding *wiacrpullv1.WIpullbinding,
	req ctrl.Request, serviceAccountName string, log logr.Logger) error {
	if containsString(acrBinding.ObjectMeta.Finalizers, wiAcrPullFinalizerName) {
		// our finalizer is present, so need to clean up ImagePullSecret reference
		var serviceAccount v1.ServiceAccount
		saNamespacedName := k8stypes.NamespacedName{
			Namespace: req.Namespace,
			Name:      serviceAccountName,
		}
		if err := r.Get(ctx, saNamespacedName, &serviceAccount); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to get service account")
				return err
			}
			log.Info("Service account is not found. Continue removing finalizer", "serviceAccountName", saNamespacedName.Name)
		} else {
			pullSecretName := getPullSecretName(acrBinding.Name)
			serviceAccount.ImagePullSecrets = removeImagePullSecretRef(serviceAccount.ImagePullSecrets, pullSecretName)
			if err := r.Update(ctx, &serviceAccount); err != nil {
				log.Error(err, "Failed to remove image pull secret reference from default service account", "pullSecretName", pullSecretName)
				return err
			}
		}

		// remove our finalizer from the list and update it.
		acrBinding.ObjectMeta.Finalizers = removeString(acrBinding.ObjectMeta.Finalizers, wiAcrPullFinalizerName)
		if err := r.Update(ctx, acrBinding); err != nil {
			log.Error(err, "Failed to remove acr pull binding finalizer", "finalizerName", wiAcrPullFinalizerName)
			return err
		}
	}
	return nil
}

func (r *WIpullbindingReconciler) setSuccessStatus(ctx context.Context, acrBinding *wiacrpullv1.WIpullbinding, accessToken types.AccessToken) error {
	tokenExp, err := accessToken.GetTokenExp()
	if err != nil {
		return err
	}

	acrBinding.Status = wiacrpullv1.WIpullbindingStatus{
		TokenExpirationTime:  &metav1.Time{Time: tokenExp},
		LastTokenRefreshTime: &metav1.Time{Time: time.Now().UTC()},
	}

	if err := r.Status().Update(ctx, acrBinding); err != nil {
		return err
	}

	return nil
}

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return result
}

func removeImagePullSecretRef(imagePullSecretRefs []v1.LocalObjectReference, secretName string) []v1.LocalObjectReference {
	var result []v1.LocalObjectReference
	for _, secretRef := range imagePullSecretRefs {
		if secretRef.Name == secretName {
			continue
		}
		result = append(result, secretRef)
	}
	return result
}

func getServiceAccountName(userSpecifiedName string) string {
	if userSpecifiedName != "" {
		return userSpecifiedName
	}
	return defaultServiceAccountName
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func getPullSecretName(acrBindingName string) string {
	return fmt.Sprintf("%s-msi-acrpull-secret", acrBindingName)
}

func getPullSecret(acrBinding *wiacrpullv1.WIpullbinding, pullSecrets []v1.Secret) *v1.Secret {
	if pullSecrets == nil {
		return nil
	}

	pullSecretName := getPullSecretName(acrBinding.Name)

	for idx, secret := range pullSecrets {
		if secret.Name == pullSecretName {
			return &pullSecrets[idx]
		}
	}

	return nil
}

func newBasePullSecret(acrBinding *wiacrpullv1.WIpullbinding,
	dockerConfig string, scheme *runtime.Scheme) (*v1.Secret, error) {

	pullSecret := &v1.Secret{
		Type: v1.SecretTypeDockerConfigJson,
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			Name:        getPullSecretName(acrBinding.Name),
			Namespace:   acrBinding.Namespace,
		},
		Data: map[string][]byte{
			dockerConfigKey: []byte(dockerConfig),
		},
	}

	if err := ctrl.SetControllerReference(acrBinding, pullSecret, scheme); err != nil {
		return nil, errors.Wrap(err, "failed to create Acr ImagePullSecret")
	}

	return pullSecret, nil
}

func updatePullSecret(pullSecret *v1.Secret, dockerConfig string) *v1.Secret {
	pullSecret.Data[dockerConfigKey] = []byte(dockerConfig)
	return pullSecret
}

func getTokenRefreshDuration(accessToken types.AccessToken) time.Duration {
	exp, err := accessToken.GetTokenExp()
	if err != nil {
		return 0
	}

	refreshDuration := exp.Sub(time.Now().Add(tokenRefreshBuffer))
	if refreshDuration < 0 {
		return 0
	}

	return refreshDuration
}

func imagePullSecretRefExist(imagePullSecretRefs []v1.LocalObjectReference, secretName string) bool {
	if imagePullSecretRefs == nil {
		return false
	}
	for _, secretRef := range imagePullSecretRefs {
		if secretRef.Name == secretName {
			return true
		}
	}
	return false
}

func appendImagePullSecretRef(serviceAccount *v1.ServiceAccount, secretName string) {
	secretReference := &v1.LocalObjectReference{
		Name: secretName,
	}
	serviceAccount.ImagePullSecrets = append(serviceAccount.ImagePullSecrets, *secretReference)
}
