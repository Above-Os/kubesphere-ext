package lldap

import (
	"errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type User struct {
	Id          string   `json:"id"`
	DisplayName string   `json:"name"`
	GroupName   []string `json:"groupName"`
}

type UserSyncer interface {
	GetProviderName() string
	Sync() ([]User, error)
}

type UserSyncManager struct {
	UserSyncers []UserSyncer
	UserSync    *iamv1alpha2.Sync
}

func GetUserSync(userSync *iamv1alpha2.Sync, client client.Client) (UserSyncManager, error) {
	syncers := make([]UserSyncer, 0)
	errs := make([]error, 0)

	for _, provider := range userSync.Spec.Providers {
		syncer, err := getUserSync(&provider, userSync, client)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		syncers = append(syncers, syncer)
	}
	return UserSyncManager{UserSyncers: syncers, UserSync: userSync}, utilerrors.NewAggregate(errs)
}

func getUserSync(provider *iamv1alpha2.Provider, userSync *iamv1alpha2.Sync, client client.Client) (UserSyncer, error) {
	if provider == nil {
		return nil, errors.New("nil userSync")
	}
	if provider.Ldap != nil {
		return &Ldap{
			Name:     provider.Name,
			Client:   client,
			UserSync: userSync,
			Provider: provider.Ldap,
		}, nil
	}
	return nil, errors.New("could not find sync")
}
