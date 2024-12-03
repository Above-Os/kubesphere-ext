package user

import (
	"context"
	"fmt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	"kubesphere.io/kubesphere/pkg/models/kubeconfig"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"kubesphere.io/kubesphere/pkg/simple/client/lldap"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"
)

const userSyncProviderKey = "iam.bytetrade.io/user-provider"

type SyncReconciler struct {
	client.Client
	KubeconfigClient        kubeconfig.Interface
	Scheme                  *runtime.Scheme
	MaxConcurrentReconciles int
}

func (r *SyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	ctrl.Log.Info("reconcilexxxxxxxxxxx request", "name", req.Name, "namespace", req.Namespace)

	obj := &iamv1alpha2.Sync{}
	err := r.Get(ctx, req.NamespacedName, obj)
	ctrl.Log.Info("get sync:", "err", err)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	userSyncManager, err := lldap.GetUserSync(obj, r.Client)
	ctrl.Log.Info("get userSyncManager:", "err", err)

	if err != nil {
		return ctrl.Result{}, err
	}

	errs := make([]error, 0)
	klog.Infof("userSyncManager.UserSyncers:%#v", userSyncManager.UserSyncers)
	for _, userSync := range userSyncManager.UserSyncers {
		ctrl.Log.Info("userSync: ", "user", userSync)
		klog.Infof("usersyncxxxx:%v", userSync)
		users, err := userSync.Sync()

		if err != nil {
			errs = append(errs, err)
			continue
		}
		fmt.Println("users:", users)

		for _, u := range users {
			userObj := &iamv1alpha2.User{}
			err = r.Get(ctx, types.NamespacedName{Name: u.Id, Namespace: ""}, userObj)
			if apierrors.IsNotFound(err) {
				userObj = &iamv1alpha2.User{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Sync",
						APIVersion: iamv1alpha2.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: u.Id,
					},
					Spec: iamv1alpha2.UserSpec{
						Name:   u.Id,
						Groups: u.GroupName,
					},
				}
			} else if err != nil {
				errs = append(errs, err)
				continue
			}
			klog.Infof("providerName:%v", userSync.GetProviderName())
			userObj.SetLabels(map[string]string{userSyncProviderKey: userSync.GetProviderName()})

			err = r.CreateOrUpdateResource(ctx, "", userObj)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			// TODO: rolebinding

		}
		err = r.pruneUsers(ctx, users, userSync.GetProviderName())
		if err != nil {
			errs = append(errs, err)
		}

		if len(errs) > 0 {
			return ctrl.Result{}, utilerrors.NewAggregate(errs)
		}
	}

	return ctrl.Result{
		RequeueAfter: time.Second * 2,
	}, nil
}

func (r *SyncReconciler) pruneUsers(ctx context.Context, syncUsers []lldap.User, providerName string) error {
	klog.Infof("pruneUsers................")
	// prune user
	opts := []client.ListOption{
		client.InNamespace(""),
		client.MatchingLabels{userSyncProviderKey: providerName},
	}
	userList := &iamv1alpha2.UserList{}
	err := r.List(ctx, userList, opts...)
	if err != nil {
		return err
	}
	klog.Infof("userList: %#v", userList)

	for _, u := range userList.Items {
		userFound := r.isUserFound(u, syncUsers)
		klog.Infof("ddelete user: %v", userFound)
		if !userFound {
			err = r.Delete(ctx, &u)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *SyncReconciler) isUserFound(targetUser iamv1alpha2.User, users []lldap.User) bool {
	for _, u := range users {
		if targetUser.Spec.Name == u.Id {
			return true
		}
	}
	return false
}

func (r *SyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	if r.MaxConcurrentReconciles <= 0 {
		r.MaxConcurrentReconciles = 1
	}

	err := ctrl.NewControllerManagedBy(mgr).
		Named("user-sync-controller").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
		}).
		For(&iamv1alpha2.Sync{}).Complete(r)
	if err != nil {
		return err
	}
	return nil
}

func (r *SyncReconciler) CreateOrUpdateResource(ctx context.Context, namespace string, obj client.Object) error {
	if namespace != "" {
		obj.SetNamespace(namespace)
	}
	obj2 := &unstructured.Unstructured{}
	obj2.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())

	err := r.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, obj2)

	if apierrors.IsNotFound(err) {
		err = r.Create(ctx, obj)
		if err != nil {
			return err
		}
		return nil
	}
	if err == nil {
		obj.SetResourceVersion(obj2.GetResourceVersion())
		err = r.Update(ctx, obj)
		if err != nil {
			return err
		}
		return nil
	}
	return err
}
